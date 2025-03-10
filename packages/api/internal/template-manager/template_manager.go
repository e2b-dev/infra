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

type TemplateManager struct {
	grpc       *GRPCClient
	db         *db.DB
	lock       sync.Mutex
	processing map[uuid.UUID]struct{ templateID string }
}

var (
	//syncInterval = time.Minute * 1
	// todo: just for testing
	syncInterval = time.Second * 10
	syncTimeout  = time.Minute * 5
	syncWaitingStateDeadline = time.Minute * 10
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
		processing: make(map[uuid.UUID]struct{ templateID string }),
	}, nil
}

func (tm *TemplateManager) Close() error {
	return tm.grpc.Close()
}

func (tm *TemplateManager) BuildsStatusPeriodicalSync() {
	for {
		buildsRunning, err := tm.db.GetRunningEnvBuilds(context.Background())
		if err != nil {
			zap.L().Error("Error getting running builds for periodical sync", zap.Error(err))
			continue
		}

		zap.L().Info("Running periodical sync of builds statuses", zap.Int("count", len(buildsRunning)))
		for _, buildDB := range buildsRunning {
			go tm.BuildStatusSync(context.Background(), buildDB.ID, *buildDB.EnvID)
		}

		time.Sleep(syncInterval)
	}
}

// BuildStatusSync todo: implement failing build when there is build in some state for too long
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

		if status.Done {
			// marked as finished but failed
			if status.Failed {
				err = tm.db.EnvBuildSetStatus(childCtx, templateID, buildID, envbuild.StatusFailed)
				if err != nil {
					logger.Error("Error when setting build status", zap.Error(err))
				}

				logger.Error("Template build failed according to status")
				return
			}

			if status.Metadata == nil {
				logger.Error("Metadata not found in template build status")
				return
			}

			err = tm.db.FinishEnvBuild(childCtx, templateID, buildID, int64(status.Metadata.RootfsSizeKey), status.Metadata.EnvdVersionKey)
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

	tm.processing[buildID] = struct{ templateID string }{templateID: templateID}
	return false
}
