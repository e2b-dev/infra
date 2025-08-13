package template_manager

import (
	"context"
	"fmt"
	"time"

	"github.com/flowchartsman/retry"
	"github.com/google/uuid"
	"github.com/pkg/errors"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	templatemanagergrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/models/envbuild"
)

var (
	buildTimeout             = time.Hour
	syncWaitingStateDeadline = time.Minute * 40
)

func (tm *TemplateManager) BuildStatusSync(ctx context.Context, buildID uuid.UUID, templateID string, clusterID *uuid.UUID, clusterNodeID *string) error {
	if tm.createInProcessingQueue(buildID, templateID) {
		// already processing, skip
		return nil
	}

	// remove from processing queue when done
	defer tm.removeFromProcessingQueue(buildID)

	envBuildDb, err := tm.db.GetEnvBuild(ctx, buildID)
	if err != nil {
		return fmt.Errorf("failed to get env build: %w", err)
	}

	// waiting for build to start, local docker build and push can take some time
	// so just check if it's not too long
	if envBuildDb.Status == envbuild.StatusWaiting {
		// if waiting for too long, fail the build
		if time.Since(envBuildDb.CreatedAt) > syncWaitingStateDeadline {
			err = tm.SetStatus(ctx, templateID, buildID, envbuild.StatusFailed, &templatemanagergrpc.TemplateBuildStatusReason{
				Message: "build is in waiting state for too long",
			})
			return fmt.Errorf("build is in waiting state for too long, failing it: %w", err)
		}

		// just wait for next sync
		return nil
	}

	checker := &PollBuildStatus{
		client: tm,
		logger: zap.L().With(logger.WithBuildID(buildID.String()), logger.WithTemplateID(templateID)),

		templateID: templateID,
		buildID:    buildID,

		clusterID:     clusterID,
		clusterNodeID: clusterNodeID,
	}

	// context for the building phase
	ctx, buildCancel := context.WithTimeout(ctx, buildTimeout)
	defer buildCancel()

	checker.poll(ctx)
	return nil
}

type templateManagerClient interface {
	SetStatus(ctx context.Context, templateID string, buildID uuid.UUID, status envbuild.Status, reason *templatemanagergrpc.TemplateBuildStatusReason) error
	SetFinished(ctx context.Context, templateID string, buildID uuid.UUID, rootfsSize int64, envdVersion string) error
	GetStatus(ctx context.Context, buildId uuid.UUID, templateID string, clusterID *uuid.UUID, nodeID *string) (*templatemanagergrpc.TemplateBuildStatusResponse, error)
}

type PollBuildStatus struct {
	logger *zap.Logger
	client templateManagerClient

	templateID string
	buildID    uuid.UUID

	clusterID     *uuid.UUID
	clusterNodeID *string

	status *templatemanagergrpc.TemplateBuildStatusResponse
}

func (c *PollBuildStatus) poll(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			c.logger.Debug("Build status polling timed out, stopping polling")

			statusErr := c.client.SetStatus(ctx, c.templateID, c.buildID, envbuild.StatusFailed, &templatemanagergrpc.TemplateBuildStatusReason{
				Message: fmt.Sprintf("build status polling timed out. Maximum build time is %s.", buildTimeout),
			})
			if statusErr != nil {
				c.logger.Error("error when setting build status", zap.Error(statusErr))
			}

			return
		case <-ticker.C:
			c.logger.Debug("Checking template build status")

			buildCompleted, err := c.checkBuildStatus(ctx)
			if err != nil {
				c.logger.Error("Build status polling received unrecoverable error", zap.Error(err))

				statusErr := c.client.SetStatus(ctx, c.templateID, c.buildID, envbuild.StatusFailed, &templatemanagergrpc.TemplateBuildStatusReason{
					Message: fmt.Sprintf("polling received unrecoverable error: %s", err),
				})
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
	status, err := c.client.GetStatus(ctx, c.buildID, c.templateID, c.clusterID, c.clusterNodeID)
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

func (c *PollBuildStatus) dispatchBasedOnStatus(ctx context.Context, status *templatemanagergrpc.TemplateBuildStatusResponse) (bool, error) {
	if status == nil {
		return false, errors.New("nil status")
	}
	switch status.GetStatus() {
	case templatemanagergrpc.TemplateBuildState_Failed:
		// build failed
		err := c.client.SetStatus(ctx, c.templateID, c.buildID, envbuild.StatusFailed, status.Reason)
		if err != nil {
			return false, errors.Wrap(err, "error when setting build status")
		}
		return true, nil
	case templatemanagergrpc.TemplateBuildState_Completed:
		// build completed
		meta := status.GetMetadata()
		if meta == nil {
			return false, errors.New("nil metadata")
		}

		err := c.client.SetFinished(ctx, c.templateID, c.buildID, int64(meta.RootfsSizeKey), meta.EnvdVersionKey)
		if err != nil {
			return false, errors.Wrap(err, "error when finishing build")
		}
		return true, nil
	default:
		c.logger.Debug("skipping status", zap.Any("status", status))
		return false, nil
	}
}

func (c *PollBuildStatus) checkBuildStatus(ctx context.Context) (bool, error) {
	c.logger.Debug("Checking template build status")

	retrier := retry.NewRetrier(
		10,
		100*time.Millisecond,
		time.Second,
	)

	err := retrier.RunContext(ctx, c.setStatus)
	if err != nil {
		c.logger.Error("error when calling setStatus", zap.Error(err))
		return false, err
	}

	c.logger.Debug("dispatching based on status", zap.Any("status", c.status))

	buildCompleted, err := c.dispatchBasedOnStatus(ctx, c.status)
	if err != nil {
		return false, errors.Wrap(err, "error when dispatching build status")
	}

	return buildCompleted, nil
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

func (tm *TemplateManager) SetStatus(ctx context.Context, templateID string, buildID uuid.UUID, status envbuild.Status, reason *templatemanagergrpc.TemplateBuildStatusReason) error {
	var apiReason *api.BuildStatusReason
	if reason != nil {
		apiReason = &api.BuildStatusReason{
			Message: reason.GetMessage(),
			Step:    reason.GetStep(),
		}
	}

	// first do database update to prevent race condition while calling status
	err := tm.db.EnvBuildSetStatus(ctx, templateID, buildID, status, utils.SerializeBuildStatusReason(apiReason))

	tm.buildCache.SetStatus(buildID, status, apiReason)
	return err
}

func (tm *TemplateManager) SetFinished(ctx context.Context, templateID string, buildID uuid.UUID, rootfsSize int64, envdVersion string) error {
	// first do database update to prevent race condition while calling status
	err := tm.db.FinishEnvBuild(ctx, templateID, buildID, rootfsSize, envdVersion)
	if err != nil {
		tm.buildCache.SetStatus(buildID, envbuild.StatusFailed, &api.BuildStatusReason{
			Message: fmt.Sprintf("error when finishing build: %s", err.Error()),
		})
		return err
	}

	tm.buildCache.SetStatus(buildID, envbuild.StatusUploaded, nil)

	return nil
}
