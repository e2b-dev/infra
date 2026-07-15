package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/orchestrator"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/auth/pkg/auth"
	"github.com/e2b-dev/infra/packages/shared/pkg/ginutils"
	"github.com/e2b-dev/infra/packages/shared/pkg/id"
	sbxlogger "github.com/e2b-dev/infra/packages/shared/pkg/logger/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	sharedUtils "github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

// maxForkCount caps how many sandboxes a single fork request can create,
// bounding the parallel boots (and the result allocation) per request.
const maxForkCount = 100

// PostSandboxesSandboxIDFork forks a running sandbox: it checkpoints the
// sandbox in place (snapshot it and resume it on its node, so the original
// keeps running with its ID and expiration untouched) and creates count new
// sandboxes from that snapshot under fresh IDs. Each fork succeeds or fails
// independently: the response carries one result per requested fork, holding
// either the created sandbox or the error that prevented it from starting.
func (a *APIStore) PostSandboxesSandboxIDFork(c *gin.Context, sandboxID api.SandboxID) {
	ctx := c.Request.Context()

	teamInfo := auth.MustGetTeamInfo(c)
	teamID := teamInfo.Team.ID

	sandboxID, err := utils.ShortID(sandboxID)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, "Invalid sandbox ID")

		return
	}

	span := trace.SpanFromContext(ctx)
	span.SetAttributes(telemetry.WithSandboxID(sandboxID))

	traceID := span.SpanContext().TraceID().String()
	c.Set("traceID", traceID)

	body, err := ginutils.ParseOptionalBody[api.PostSandboxesSandboxIDForkJSONRequestBody](ctx, c)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Error when parsing request: %s", err))

		return
	}

	forkTimeout := sandbox.SandboxTimeoutDefault
	if body.Timeout != nil {
		forkTimeout = time.Duration(*body.Timeout) * time.Second

		if forkTimeout > time.Duration(teamInfo.Limits.MaxLengthHours)*time.Hour {
			a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Timeout cannot be greater than %d hours", teamInfo.Limits.MaxLengthHours))

			return
		}
	}

	forkCount := 1
	if body.Count != nil {
		forkCount = int(*body.Count)
	}

	if forkCount < 1 {
		a.sendAPIStoreError(c, http.StatusBadRequest, "Count must be at least 1")

		return
	}

	if forkCount > maxForkCount {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Count cannot be greater than %d", maxForkCount))

		return
	}

	// The original sandbox keeps running and holds one slot, so more forks
	// than the concurrency limit can never succeed.
	if int64(forkCount) >= teamInfo.Limits.SandboxConcurrency {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Count must be lower than the maximum number of concurrent sandboxes (%d)", teamInfo.Limits.SandboxConcurrency))

		return
	}

	original, err := a.orchestrator.GetSandbox(ctx, teamID, sandboxID)
	if err != nil {
		if errors.Is(err, sandbox.ErrNotFound) {
			apiErr := forkHandleNotRunningSandbox(ctx, a, sandboxID, teamID)
			a.sendAPIStoreError(c, apiErr.Code, apiErr.ClientMsg)

			return
		}

		telemetry.ReportError(ctx, "error getting sandbox for fork", err, telemetry.WithSandboxID(sandboxID))
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error forking sandbox")

		return
	}

	if err := sharedUtils.CheckEnvdVersionForSnapshot(original.EnvdVersion); err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, err.Error())

		return
	}

	// Checkpoint the sandbox in place: it is briefly paused on its node,
	// snapshotted, and resumed under the same execution ID, so the original
	// keeps its ID, expiration, and concurrency slot.
	err = a.orchestrator.CheckpointSandbox(ctx, teamID, sandboxID)
	var transErr *sandbox.InvalidStateTransitionError

	switch {
	case err == nil:
	case errors.Is(err, sandbox.ErrNotFound):
		apiErr := forkHandleNotRunningSandbox(ctx, a, sandboxID, teamID)
		a.sendAPIStoreError(c, apiErr.Code, apiErr.ClientMsg)

		return
	case errors.As(err, &transErr):
		a.sendAPIStoreError(c, http.StatusConflict, fmt.Sprintf("Sandbox '%s' cannot be forked while in '%s' state", sandboxID, transErr.CurrentState))

		return
	case errors.Is(err, orchestrator.PauseQueueExhaustedError{}):
		a.sendAPIStoreError(c, http.StatusServiceUnavailable, fmt.Sprintf("Sandbox '%s' cannot be forked right now because its node is busy, please retry", sandboxID))

		return
	default:
		telemetry.ReportError(ctx, "error checkpointing sandbox for fork", err, telemetry.WithSandboxID(sandboxID))
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error forking sandbox")

		return
	}

	sbxlogger.E(&sbxlogger.SandboxMetadata{
		SandboxID:  sandboxID,
		TemplateID: original.TemplateID,
		TeamID:     teamID.String(),
	}).Debug(ctx, "Creating forked sandboxes from snapshot", zap.Int("count", forkCount))

	// All forks boot in parallel from the same immutable snapshot, each
	// succeeding or failing independently.
	results := make([]api.SandboxForkResult, forkCount)

	wg := errgroup.Group{}
	for i := range forkCount {
		wg.Go(func() error {
			forkedSandboxID := InstanceIDPrefix + id.Generate()

			forkedSbx, createErr := a.startSandbox(
				ctx,
				forkedSandboxID,
				forkTimeout,
				teamInfo,
				a.buildResumeSandboxDataFromSnapshot(sandboxID, forkedSandboxID, nil),
				&c.Request.Header,
				true,
				nil, // mcp
			)
			if createErr != nil {
				telemetry.ReportError(ctx, "error creating forked sandbox", createErr.Err, telemetry.WithSandboxID(forkedSandboxID))
				results[i] = api.SandboxForkResult{Error: &api.Error{Code: int32(createErr.Code), Message: createErr.ClientMsg}}

				//nolint:nilerr // per-fork errors are reported in the result entry, not propagated
				return nil
			}

			results[i] = api.SandboxForkResult{Sandbox: forkedSbx}

			return nil
		})
	}
	_ = wg.Wait()

	c.JSON(http.StatusCreated, results)
}

// forkHandleNotRunningSandbox classifies a fork request for a sandbox that is
// not running: 409 if it is paused (a snapshot exists), 404 otherwise.
func forkHandleNotRunningSandbox(ctx context.Context, a *APIStore, sandboxID string, teamID uuid.UUID) api.APIError {
	apiErr := pauseHandleNotRunningSandbox(ctx, a.snapshotCache, sandboxID, teamID)
	switch apiErr.Code {
	case http.StatusConflict:
		apiErr.ClientMsg = fmt.Sprintf("Sandbox '%s' is paused and cannot be forked; resume it first", sandboxID)
	case http.StatusInternalServerError:
		apiErr.ClientMsg = "Error forking sandbox"
	}

	return apiErr
}
