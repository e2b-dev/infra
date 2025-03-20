package template_manager

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	templatecache "github.com/e2b-dev/infra/packages/api/internal/cache/templates"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/shared/pkg/db"
	template_manager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	"github.com/e2b-dev/infra/packages/shared/pkg/models/envbuild"
)

type processingBuilds struct {
	templateID string
}

type TemplateManager struct {
	grpc       *GRPCClient
	db         *db.DB
	lock       sync.Mutex
	processing map[uuid.UUID]processingBuilds
	buildCache *templatecache.TemplatesBuildCache
}

var (
	syncInterval             = time.Minute * 1
	syncTimeout              = time.Minute * 5
	syncWaitingStateDeadline = time.Minute * 20
)

func New(db *db.DB, buildCache *templatecache.TemplatesBuildCache) (*TemplateManager, error) {
	client, err := NewClient()
	if err != nil {
		return nil, err
	}

	return &TemplateManager{
		grpc:       client,
		db:         db,
		lock:       sync.Mutex{},
		processing: make(map[uuid.UUID]processingBuilds),
		buildCache: buildCache,
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
			err = tm.SetStatus(childCtx, templateID, buildID, envbuild.StatusFailed, "build is in waiting state for too long")
			if err != nil {
				logger.Error("Error when setting build status", zap.Error(err))
			}

			return
		}

		// just wait for next sync
		return
	}

	retries := 5

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	// poll build status
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			logger.Info("Checking template build status")

			status, err := tm.grpc.Client.TemplateBuildStatus(childCtx, &template_manager.TemplateStatusRequest{TemplateID: templateID, BuildID: buildID.String()})
			if utils.UnwrapGRPCError(err) != nil {
				logger.Error("Error when fetching template build status", zap.Error(err))
				retries--
				if retries == 0 {
					err = tm.SetStatus(childCtx, templateID, buildID, envbuild.StatusFailed, fmt.Sprintf("error when fetching template build status: %s", err))
					if err != nil {
						logger.Error("Error when setting build status", zap.Error(err))
					}

					return
				}
				continue
			}
			if err == nil && status != nil {
				// reset retries when we get a valid status
				retries = 5
			}

			// defensive against nil pointer dereference
			if status == nil {
				retries--
				if retries == 0 {
					err = tm.SetStatus(childCtx, templateID, buildID, envbuild.StatusFailed, "error when fetching template build status: nil status")
					if err != nil {
						logger.Error("Error when setting build status", zap.Error(err))
					}
				}
				continue
			}

			// build failed
			if status.GetStatus() == template_manager.TemplateBuildState_Failed {
				logger.Error("Template build failed according to status")
				err = tm.SetStatus(childCtx, templateID, buildID, envbuild.StatusFailed, "template build failed according to status")
				if err != nil {
					logger.Error("Error when setting build status", zap.Error(err))
				}

				return
			}

			// build completed
			if status.GetStatus() == template_manager.TemplateBuildState_Completed {
				meta := status.GetMetadata()
				err = tm.SetFinished(childCtx, templateID, buildID, int64(meta.RootfsSizeKey), meta.EnvdVersionKey)
				if err != nil {
					logger.Error("Error when finishing build", zap.Error(err))
					return
				}

				logger.Info("Template build finished")
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

func (tm *TemplateManager) SetStatus(ctx context.Context, templateID string, buildID uuid.UUID, status envbuild.Status, reason string) error {
	// first do database update to prevent race condition while calling status
	err := tm.db.EnvBuildSetStatus(ctx, templateID, buildID, status)
	tm.buildCache.SetStatus(buildID, status, reason)
	return err
}

func (tm *TemplateManager) SetFinished(ctx context.Context, templateID string, buildID uuid.UUID, rootfsSize int64, envdVersion string) error {
	// first do database update to prevent race condition while calling status
	err := tm.db.FinishEnvBuild(ctx, templateID, buildID, rootfsSize, envdVersion)

	if err != nil {
		tm.buildCache.SetStatus(buildID, envbuild.StatusFailed, "error when finishing build")
		return err
	}

	tm.buildCache.SetStatus(buildID, envbuild.StatusUploaded, "build finished")

	return nil
}
