package template_manager

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/flowchartsman/retry"
	"github.com/google/uuid"
	"github.com/pkg/errors"
	"go.uber.org/zap"
	"google.golang.org/grpc"

	templatecache "github.com/e2b-dev/infra/packages/api/internal/cache/templates"
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

const (
	syncInterval             = time.Minute * 1
	syncTimeout              = time.Minute * 15
	syncWaitingStateDeadline = time.Minute * 40
)

func New(ctx context.Context, db *db.DB, buildCache *templatecache.TemplatesBuildCache) (*TemplateManager, error) {
	client, err := NewClient(ctx)
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
				logger.Error("error when setting build status", zap.Error(err))
			}

			return
		}

		// just wait for next sync
		return
	}

	checker := &PollBuildStatus{
		statusClient:          tm.grpc.TemplateClient,
		logger:                logger,
		templateID:            templateID,
		buildID:               buildID,
		templateManagerClient: tm,
	}

	checker.poll(ctx)
}

type statusClient interface {
	TemplateBuildStatus(ctx context.Context, in *template_manager.TemplateStatusRequest, opts ...grpc.CallOption) (*template_manager.TemplateBuildStatusResponse, error)
}

type templateManagerClient interface {
	SetStatus(ctx context.Context, templateID string, buildID uuid.UUID, status envbuild.Status, reason string) error
	SetFinished(ctx context.Context, templateID string, buildID uuid.UUID, rootfsSize int64, envdVersion string) error
}

type PollBuildStatus struct {
	statusClient          statusClient
	logger                *zap.Logger
	templateID            string
	buildID               uuid.UUID
	templateManagerClient templateManagerClient
	status                *template_manager.TemplateBuildStatusResponse
}

func (c *PollBuildStatus) poll(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.logger.Info("Checking template build status")

			err, buildCompleted := c.checkBuildStatus(ctx)
			if err != nil {
				c.logger.Error("Build status polling received unrecoverable error", zap.Error(err))

				statusErr := c.templateManagerClient.SetStatus(ctx, c.templateID, c.buildID, envbuild.StatusFailed, fmt.Sprintf("polling received unrecoverable error: %s", err))
				if statusErr != nil {
					c.logger.Error("error when setting build status", zap.Error(statusErr))
				}
				return
			}

			// build status can return empty error when build is still in progress
			// this will cause fast return to avoid pooling when build is already finished
			if buildCompleted {
				return
			}
		}
	}
}

// terminalError is a terminal error that should not be retried
// set like this so that we can check for it using errors.As
type terminalError struct {
	err error
}

func (e terminalError) Error() string {
	return e.err.Error()
}

func newTerminalError(err error) error {
	return terminalError{
		err: retry.Stop(errors.WithStack(err)),
	}
}

func (c *PollBuildStatus) setStatus(ctx context.Context) error {
	if c.statusClient == nil {
		return errors.New("status client is nil")
	}

	status, err := c.statusClient.TemplateBuildStatus(ctx, &template_manager.TemplateStatusRequest{TemplateID: c.templateID, BuildID: c.buildID.String()})

	if err != nil && errors.Is(err, context.DeadlineExceeded) {
		return errors.Wrap(err, "context deadline exceeded")
	} else if err != nil { // retry only on context deadline exceeded
		c.logger.Error("terminal error when polling build status", zap.Error(err))
		return newTerminalError(err)
	}

	if status == nil {
		return errors.New("nil status") // this should never happen
	}
	// debug log the status
	c.logger.Debug("setting status pointer", zap.Any("status", status))

	c.status = status
	return nil

}

func (c *PollBuildStatus) dispatchBasedOnStatus(ctx context.Context, status *template_manager.TemplateBuildStatusResponse) (error, bool) {
	if status == nil {
		return errors.New("nil status"), false
	}
	switch status.GetStatus() {
	case template_manager.TemplateBuildState_Failed:
		// build failed
		err := c.templateManagerClient.SetStatus(ctx, c.templateID, c.buildID, envbuild.StatusFailed, "template build failed according to status")
		if err != nil {
			return errors.Wrap(err, "error when setting build status"), false
		}
		return nil, true
	case template_manager.TemplateBuildState_Completed:
		// build completed
		meta := status.GetMetadata()
		if meta == nil {
			return errors.New("nil metadata"), false
		}

		err := c.templateManagerClient.SetFinished(ctx, c.templateID, c.buildID, int64(meta.RootfsSizeKey), meta.EnvdVersionKey)
		if err != nil {
			return errors.Wrap(err, "error when finishing build"), false
		}
		return nil, true
	default:
		c.logger.Debug("skipping status", zap.Any("status", status))
		return nil, false
	}
}

func (c *PollBuildStatus) checkBuildStatus(ctx context.Context) (error, bool) {
	c.logger.Info("Checking template build status")

	retrier := retry.NewRetrier(
		10,
		100*time.Millisecond,
		time.Second,
	)

	err := retrier.RunContext(ctx, c.setStatus)
	if err != nil {
		c.logger.Error("error when calling setStatus", zap.Error(err))
		return err, false
	}

	c.logger.Debug("dispatching based on status", zap.Any("status", c.status))

	err, buildCompleted := c.dispatchBasedOnStatus(ctx, c.status)
	if err != nil {
		return errors.Wrap(err, "error when dispatching build status"), false
	}

	return nil, buildCompleted
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
