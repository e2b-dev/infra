package template_manager

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/flowchartsman/retry"
	"github.com/google/uuid"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	"github.com/e2b-dev/infra/packages/db/pkg/types"
	"github.com/e2b-dev/infra/packages/db/queries"
	templatemanagergrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

var (
	buildTimeout             = time.Hour
	syncWaitingStateDeadline = time.Minute * 40

	// transientErrorGracePeriod is how long the poller keeps asking the builder
	// for a build status that only answers with transient errors. The build
	// itself keeps running on the builder while we retry, so failing it on the
	// first hiccup would throw away work that is still perfectly fine.
	transientErrorGracePeriod = 5 * time.Minute
)

// errTransientStatus marks a status error that is worth retrying instead of
// failing the build over.
var errTransientStatus = errors.New("transient error")

func (tm *TemplateManager) BuildStatusSync(ctx context.Context, buildID uuid.UUID, templateID string, clusterID uuid.UUID, nodeID *string) error {
	if tm.createInProcessingQueue(buildID, templateID) {
		// already processing, skip
		return nil
	}

	// remove from processing queue when done
	defer tm.removeFromProcessingQueue(buildID)

	result, err := tm.sqlcDB.GetTemplateBuildWithTemplate(ctx, queries.GetTemplateBuildWithTemplateParams{
		TemplateID: templateID,
		BuildID:    buildID,
	})
	if err != nil {
		return fmt.Errorf("failed to get env build: %w", err)
	}

	envBuild := result.EnvBuild
	// waiting for build to start, local docker build and push can take some time
	// so just check if it's not too long
	if envBuild.StatusGroup == types.BuildStatusGroupPending {
		// if waiting for too long, fail the build
		if time.Since(envBuild.CreatedAt) > syncWaitingStateDeadline {
			err = tm.SetStatus(ctx, buildID, types.BuildStatusGroupFailed, &templatemanagergrpc.TemplateBuildStatusReason{
				Message: "build is in waiting state for too long",
			})
			if err != nil {
				logger.L().Error(ctx, "error when setting build status to failed after waiting for too long", zap.Error(err), logger.WithBuildID(buildID.String()), logger.WithTemplateID(templateID))
			}

			return errors.New("build is in waiting state for too long, failing it")
		}

		// just wait for next sync
		return nil
	}

	if nodeID == nil {
		return errors.New("build is not assigned to a node, but it should be")
	}

	checker := &PollBuildStatus{
		client: tm,
		logger: logger.L().With(logger.WithBuildID(buildID.String()), logger.WithTemplateID(templateID)),

		templateID: templateID,
		buildID:    buildID,

		clusterID: clusterID,
		nodeID:    *nodeID,
	}

	// context for the building phase
	ctx, buildCancel := context.WithTimeout(ctx, buildTimeout)
	defer buildCancel()

	checker.poll(ctx)

	return nil
}

type templateManagerClient interface {
	SetStatus(ctx context.Context, buildID uuid.UUID, statusGroup types.BuildStatusGroup, reason *templatemanagergrpc.TemplateBuildStatusReason) error
	SetFinished(ctx context.Context, buildID uuid.UUID, rootfsSize int64, envdVersion, kernelVersion, firecrackerVersion string) error
	GetStatus(ctx context.Context, buildId uuid.UUID, templateID string, clusterID uuid.UUID, nodeID string) (*templatemanagergrpc.TemplateBuildStatusResponse, error)
}

type PollBuildStatus struct {
	logger logger.Logger
	client templateManagerClient

	templateID string
	buildID    uuid.UUID

	clusterID uuid.UUID
	nodeID    string

	status *templatemanagergrpc.TemplateBuildStatusResponse

	// transientErrorsSince is when the current run of transient status errors
	// started. Zero while the builder is answering.
	transientErrorsSince time.Time
}

func (c *PollBuildStatus) poll(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			c.logger.Debug(ctx, "Build status polling timed out, stopping polling")

			statusErr := c.client.SetStatus(ctx, c.buildID, types.BuildStatusGroupFailed, &templatemanagergrpc.TemplateBuildStatusReason{
				Message: fmt.Sprintf("build status polling timed out. Maximum build time is %s.", buildTimeout),
			})
			if statusErr != nil {
				c.logger.Error(ctx, "error when setting build status", zap.Error(statusErr))
			}

			return
		case <-ticker.C:
			buildCompleted, err := c.checkBuildStatus(ctx)
			if err != nil {
				failure := c.buildFailure(err)
				if failure == nil {
					c.logger.Warn(ctx, "Build status polling received a transient error, keeping the build alive", zap.Error(err))

					continue
				}

				c.logger.Error(ctx, "Build status polling failed", zap.Error(failure))

				statusErr := c.client.SetStatus(ctx, c.buildID, types.BuildStatusGroupFailed, &templatemanagergrpc.TemplateBuildStatusReason{
					Message: failure.Error(),
				})
				if statusErr != nil {
					c.logger.Error(ctx, "error when setting build status", zap.Error(statusErr))
				}

				return
			}

			c.transientErrorsSince = time.Time{}

			// build status can return empty error when build is still in progress
			// this will cause fast return to avoid pooling when build is already finished
			if buildCompleted {
				return
			}
		}
	}
}

// buildFailure turns a polling error into the reason the build should be failed
// with, or nil when the error is transient and the poller should just try again.
func (c *PollBuildStatus) buildFailure(err error) error {
	if !errors.Is(err, errTransientStatus) {
		return fmt.Errorf("polling received unrecoverable error: %w", err)
	}

	if c.transientErrorsSince.IsZero() {
		c.transientErrorsSince = time.Now()
	}

	// Give up once the builder has been unreachable for long enough that the
	// build is very unlikely to still be alive on the other end.
	if time.Since(c.transientErrorsSince) >= transientErrorGracePeriod {
		return fmt.Errorf("polling kept failing for %s: %w", transientErrorGracePeriod, err)
	}

	return nil
}

// isTransientStatusError reports whether a status RPC failed for a reason that
// says nothing about the build itself: the call timed out, or the builder was
// briefly unreachable or overloaded. gRPC status errors do not unwrap to
// context.DeadlineExceeded, so the status code has to be inspected explicitly.
func isTransientStatusError(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}

	st, ok := grpcstatus.FromError(err)
	if !ok {
		return false
	}

	switch st.Code() {
	case codes.DeadlineExceeded, codes.Unavailable, codes.ResourceExhausted:
		return true
	default:
		return false
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
		err: retry.Stop(err),
	}
}

func (c *PollBuildStatus) setStatus(ctx context.Context) error {
	status, err := c.client.GetStatus(ctx, c.buildID, c.templateID, c.clusterID, c.nodeID)
	if err != nil && isTransientStatusError(err) {
		return fmt.Errorf("%w when polling build status: %w", errTransientStatus, err)
	} else if err != nil { // retry only on transient errors
		c.logger.Error(ctx, "terminal error when polling build status", zap.Error(err))

		return newTerminalError(err)
	}

	if status == nil {
		return errors.New("nil status") // this should never happen
	}

	// debug log the status
	c.logger.Debug(ctx, "setting status pointer", zap.String("status", status.GetStatus().String()))

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
		err := c.client.SetStatus(ctx, c.buildID, types.BuildStatusGroupFailed, status.GetReason())
		if err != nil {
			return false, fmt.Errorf("error when setting build status: %w", err)
		}

		return true, nil
	case templatemanagergrpc.TemplateBuildState_Completed:
		// build completed
		meta := status.GetMetadata()
		if meta == nil {
			return false, errors.New("nil metadata")
		}

		err := c.client.SetFinished(
			ctx,
			c.buildID,
			int64(meta.GetRootfsSizeKey()),
			meta.GetEnvdVersionKey(),
			meta.GetKernelVersion(),
			meta.GetFirecrackerVersion(),
		)
		if err != nil {
			return false, fmt.Errorf("error when finishing build: %w", err)
		}

		return true, nil
	default:
		c.logger.Debug(ctx, "skipping status", zap.String("status", status.GetStatus().String()))

		return false, nil
	}
}

func (c *PollBuildStatus) checkBuildStatus(ctx context.Context) (bool, error) {
	c.logger.Debug(ctx, "Checking template build status")

	retrier := retry.NewRetrier(
		10,
		100*time.Millisecond,
		time.Second,
	)

	// The caller logs the error with the level matching how it handles it.
	err := retrier.RunContext(ctx, c.setStatus)
	if err != nil {
		return false, err
	}

	c.logger.Debug(ctx, "dispatching based on status", zap.String("status", c.status.GetStatus().String()))

	buildCompleted, err := c.dispatchBasedOnStatus(ctx, c.status)
	if err != nil {
		return false, fmt.Errorf("error when dispatching build status: %w", err)
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

func (tm *TemplateManager) SetStatus(ctx context.Context, buildID uuid.UUID, statusGroup types.BuildStatusGroup, reason *templatemanagergrpc.TemplateBuildStatusReason) error {
	var buildReason types.BuildReason
	if reason != nil {
		buildReason = types.BuildReason{
			Message: reason.GetMessage(),
		}
		if step := reason.GetStep(); step != "" {
			buildReason.Step = &step
		}
	}

	now := time.Now()

	var err error
	if statusGroup.IsTerminal() {
		logger.L().Warn(ctx, "Setting template build status to terminal failure",
			logger.WithBuildID(buildID.String()),
			zap.String("reason", buildReason.Message),
		)

		err = tm.sqlcDB.FailTemplateBuildAndDeactivate(ctx, queries.FailTemplateBuildAndDeactivateParams{
			Status:     buildStatus(statusGroup),
			FinishedAt: &now,
			Reason:     buildReason,
			BuildID:    buildID,
		})
	} else {
		err = tm.sqlcDB.UpdateEnvBuildStatus(ctx, queries.UpdateEnvBuildStatusParams{
			Status:     buildStatus(statusGroup),
			FinishedAt: &now,
			Reason:     buildReason,
			BuildID:    buildID,
		})
	}

	tm.buildCache.Invalidate(ctx, buildID)

	return err
}

func (tm *TemplateManager) SetFinished(ctx context.Context, buildID uuid.UUID, rootfsSize int64, envdVersion, kernelVersion, firecrackerVersion string) error {
	// first do database update to prevent race condition while calling status
	// TODO(ENG-3469): Switch to types.BuildStatusReady once all consumers are migrated.
	err := tm.sqlcDB.FinishTemplateBuild(ctx, queries.FinishTemplateBuildParams{
		TotalDiskSizeMb:    &rootfsSize,
		Status:             types.BuildStatusUploaded,
		EnvdVersion:        &envdVersion,
		KernelVersion:      kernelVersion,
		FirecrackerVersion: firecrackerVersion,
		BuildID:            buildID,
	})

	tm.buildCache.Invalidate(ctx, buildID)

	return err
}

// buildStatus maps a status group to a default build status for the database.
func buildStatus(g types.BuildStatusGroup) types.BuildStatus {
	switch g {
	case types.BuildStatusGroupPending:
		return types.BuildStatusPending
	case types.BuildStatusGroupInProgress:
		return types.BuildStatusBuilding
	case types.BuildStatusGroupReady:
		return types.BuildStatusUploaded
	case types.BuildStatusGroupFailed:
		return types.BuildStatusFailed
	default:
		return types.BuildStatusFailed
	}
}
