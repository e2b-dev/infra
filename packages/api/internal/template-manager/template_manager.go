package template_manager

import (
	"context"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/shared/pkg/db"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	"github.com/e2b-dev/infra/packages/shared/pkg/models/envbuild"
	"github.com/google/uuid"
	"go.uber.org/zap"
	"io"
	"sync"
	"time"
)

type processingBuilds struct {
	templateID string
}

type TemplateManager struct {
	grpc       *GRPCClient
	db         *db.DB
	lock       sync.Mutex
	processing map[uuid.UUID]processingBuilds
}

var (
	syncInterval             = time.Minute * 1
	syncTimeout              = time.Minute * 5
	syncWaitingStateDeadline = time.Minute * 20
)

func New(db *db.DB) (*TemplateManager, error) {
	client, err := NewClient()
	if err != nil {
		return nil, err
	}

	return &TemplateManager{
		grpc:       client,
		db:         db,
		lock:       sync.Mutex{},
		processing: make(map[uuid.UUID]processingBuilds),
	}, nil
}

func (tm *TemplateManager) Close() error {
	return tm.grpc.Close()
}

func (tm *TemplateManager) BuildsStatusPeriodicalSync(ctx context.Context) {
	ticker := time.NewTicker(syncInterval)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			dbCtx, dbxCtxCancel := context.WithTimeout(ctx, 5*time.Second)
			buildsRunning, err := tm.db.GetRunningEnvBuilds(dbCtx)
			if err != nil {
				zap.L().Error("Error getting running builds for periodical sync", zap.Error(err))
				dbxCtxCancel()
				continue
			}

			zap.L().Info("Running periodical sync of builds statuses", zap.Int("count", len(buildsRunning)))
			for _, buildDB := range buildsRunning {
				go tm.BuildStatusSync(ctx, buildDB.ID, *buildDB.EnvID)
			}

			dbxCtxCancel()
		}
	}
}

func (tm *TemplateManager) BuildStatusSync(ctx context.Context, buildID uuid.UUID, templateID string) {
	childCtx, childCtxCancel := context.WithTimeout(ctx, syncTimeout)
	defer childCtxCancel()

	if tm.createInProcessingQueue(buildID, templateID) {
		// already processing, skip
		return
	}

	// remove from processing queue when done
	defer tm.removeFromProcessingQueue(buildID)

	logger := zap.L().With(zap.String("buildID", buildID.String()), zap.String("envID", templateID))

	envBuildDb, err := tm.db.GetEnvBuild(childCtx, buildID)
	if err != nil {
		logger.Error("Error when fetching env build for background sync", zap.Error(err))
		return
	}

	// waiting for build to start, local docker build and push can take some time
	// so just check if it's not too long
	if envBuildDb.Status == envbuild.StatusWaiting {
		// if waiting for too long, fail the build
		if time.Since(envBuildDb.CreatedAt) > syncWaitingStateDeadline {
			logger.Error("Build is in waiting state for too long, failing it")

			dbErr := tm.db.EnvBuildSetStatus(childCtx, templateID, buildID, envbuild.StatusFailed)
			if dbErr != nil {
				logger.Error("Error when setting build status", zap.Error(dbErr))
			}
			return
		}
	}

	// stream build status
	stream, err := tm.grpc.Client.TemplateBuildStatus(childCtx, &template_manager.TemplateStatusRequest{BuildID: buildID.String(), TemplateID: templateID})
	if utils.UnwrapGRPCError(err) != nil {
		logger.Error("Error when fetching template build status", zap.Error(err))

		dbErr := tm.db.EnvBuildSetStatus(childCtx, templateID, buildID, envbuild.StatusFailed)
		if dbErr != nil {
			logger.Error("Error when setting build status", zap.Error(dbErr))
		}

		return
	}

	for {
		status, receiveErr := stream.Recv()
		if receiveErr == io.EOF {
			break
		} else if receiveErr != nil {
			logger.Error("Error when receiving template build status", zap.Error(receiveErr))

			err = tm.db.EnvBuildSetStatus(childCtx, templateID, buildID, envbuild.StatusFailed)
			if err != nil {
				logger.Error("Error when setting build status", zap.Error(err))
			}

			return
		}

		// build failed
		if status.GetStatus() == template_manager.TemplateBuildState_Failed {
			err = tm.db.EnvBuildSetStatus(childCtx, templateID, buildID, envbuild.StatusFailed)
			if err != nil {
				logger.Error("Error when setting build status", zap.Error(err))
			}

			logger.Error("Template build failed according to status")
			return
		}

		// build completed
		if status.GetStatus() == template_manager.TemplateBuildState_Completed {
			meta := status.GetMetadata()
			err = tm.db.FinishEnvBuild(childCtx, templateID, buildID, int64(meta.RootfsSizeKey), meta.EnvdVersionKey)
			if err != nil {
				logger.Error("Error when finishing build", zap.Error(err))
				return
			}
		}
	}
}

func (tm *TemplateManager) removeFromProcessingQueue(buildID uuid.UUID) {
	tm.lock.Lock()
	delete(tm.processing, buildID)
	tm.lock.Unlock()
}

func (tm *TemplateManager) createInProcessingQueue(buildID uuid.UUID, templateID string) bool {
	tm.lock.Lock()
	defer tm.lock.Unlock()

	_, exists := tm.processing[buildID]
	if exists {
		// already in processing queue, skip
		return true
	}

	tm.processing[buildID] = processingBuilds{templateID: templateID}
	return false
}
