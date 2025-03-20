package template_manager

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/pkg/errors"
	"go.uber.org/zap"
	"google.golang.org/grpc"

	templatecache "github.com/e2b-dev/infra/packages/api/internal/cache/templates"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/shared/pkg/db"
	template_manager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	"github.com/e2b-dev/infra/packages/shared/pkg/models/envbuild"
	"github.com/flowchartsman/retry"
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

	checker := &PollBuildStatus{
		statusClient: tm.grpc.Client,
		ctx:          childCtx,
		// Before Go 1.23, the garbage collector did not recover
		// tickers that had not yet expired or been stopped, so code often
		// immediately deferred t.Stop after calling NewTicker, to make
		// the ticker recoverable when it was no longer needed.
		// As of Go 1.23, the garbage collector can recover unreferenced
		// tickers, even if they haven't been stopped.
		// The Stop method is no longer necessary to help the garbage collector.
		// (Code may of course still want to call Stop to stop the ticker for other reasons.)
		tickChannel:           time.NewTicker(time.Second).C,
		logger:                logger,
		templateID:            templateID,
		buildID:               buildID,
		templateManagerClient: tm,
	}

	checker.poll()

}

type statusClient interface {
	TemplateBuildStatus(ctx context.Context, in *template_manager.TemplateStatusRequest, opts ...grpc.CallOption) (*template_manager.TemplateBuildStatusResponse, error)
}

type templateManagerClient interface {
	SetStatus(ctx context.Context, templateID string, buildID uuid.UUID, status envbuild.Status, reason string) error
	SetFinished(ctx context.Context, templateID string, buildID uuid.UUID, rootfsSize int64, envdVersion string) error
}

type PollBuildStatus struct {
	retries               int
	retryInterval         time.Duration
	tickChannel           <-chan time.Time
	ctx                   context.Context
	statusClient          statusClient
	logger                *zap.Logger
	templateID            string
	buildID               uuid.UUID
	templateManagerClient templateManagerClient
}

func (c *PollBuildStatus) poll() {

	// poll build status
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-c.tickChannel:
			err := c.setBuildStatus()
			if utils.UnwrapGRPCError(err) != nil {
				c.logger.Error("Error when polling build status", zap.Error(err))
				err = c.templateManagerClient.SetStatus(
					c.ctx,
					c.templateID,
					c.buildID,
					envbuild.StatusFailed,
					err.Error(),
				)
				if err != nil {
					c.logger.Error("Error when setting build status", zap.Error(err))
				}
				return
			}

		}
	}
}

func (c *PollBuildStatus) getFuncToRetry(s *template_manager.TemplateBuildStatusResponse) func() error {
	return func() error {
		if c.statusClient == nil {
			return errors.New("status client is nil")
		}
		status, err := c.statusClient.TemplateBuildStatus(c.ctx, &template_manager.TemplateStatusRequest{TemplateID: c.templateID, BuildID: c.buildID.String()})

		if err != nil && strings.Contains(err.Error(), "context deadline exceeded") {
			return err
		} else if err != nil { // retry only on context deadline exceeded
			return retry.Stop(errors.Wrap(err, "error when polling build status"))
		}

		if status == nil {
			return errors.New("nil status") // this should never happen
		}

		s = status // update the status pointer if we got a new one

		return nil
	}
}

func (c *PollBuildStatus) setBuildStatus() error {
	c.logger.Info("Checking template build status")

	retrier := retry.NewRetrier(c.retries, 100*time.Millisecond, time.Second)
	var status *template_manager.TemplateBuildStatusResponse
	err := retrier.Run(c.getFuncToRetry(status))
	if err != nil {
		return errors.Wrap(err, "error when polling build status")
	}

	switch status.GetStatus() {
	case template_manager.TemplateBuildState_Failed:
		// build failed
		err = c.templateManagerClient.SetStatus(c.ctx, c.templateID, c.buildID, envbuild.StatusFailed, "template build failed according to status")
		return errors.Wrap(err, "error when setting build status")

	case template_manager.TemplateBuildState_Completed:
		// build completed
		meta := status.GetMetadata()
		err = c.templateManagerClient.SetFinished(c.ctx, c.templateID, c.buildID, int64(meta.RootfsSizeKey), meta.EnvdVersionKey)
		if err != nil {
			return errors.Wrap(err, "error when finishing build")
		}
		return nil

	default:
		// don't error on unknown build status so things don't randomly break
		return nil
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
