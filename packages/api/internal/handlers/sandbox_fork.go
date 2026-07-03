package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/sync/errgroup"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/orchestrator"
	"github.com/e2b-dev/infra/packages/api/internal/pause"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/auth/pkg/auth"
	"github.com/e2b-dev/infra/packages/shared/pkg/ginutils"
	"github.com/e2b-dev/infra/packages/shared/pkg/id"
	sbxlogger "github.com/e2b-dev/infra/packages/shared/pkg/logger/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

// PostSandboxesSandboxIDFork forks a running sandbox: it pauses the sandbox to
// a snapshot, resumes the original sandbox from that snapshot so it keeps
// running under its original ID and remaining time to live, and creates a new
// sandbox from the same snapshot. It returns the newly created sandbox.
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
	filesystemOnly := body.Memory != nil && !*body.Memory

	// Capture the original sandbox's expiration before pausing it, so resuming
	// it afterwards doesn't change its expiration.
	original, err := a.orchestrator.GetSandbox(ctx, teamID, sandboxID)
	if err != nil {
		if errors.Is(err, sandbox.ErrNotFound) {
			apiErr := pauseHandleNotRunningSandbox(ctx, a.snapshotCache, sandboxID, teamID)
			a.sendAPIStoreError(c, apiErr.Code, apiErr.ClientMsg)

			return
		}

		telemetry.ReportError(ctx, "error getting sandbox for fork", err, telemetry.WithSandboxID(sandboxID))
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error forking sandbox")

		return
	}
	originalEndTime := original.EndTime

	// Fork ends with two running sandboxes, but the authoritative concurrency
	// check at sandbox creation runs only after the original was paused. Check
	// the limit up front (best-effort; creation still enforces it) so we don't
	// pause the original only to fail creating the fork.
	teamSandboxes, err := a.orchestrator.GetSandboxes(ctx, teamID, nil)
	switch {
	case err != nil:
		telemetry.ReportError(ctx, "error listing team sandboxes before fork", err)
	case int64(len(teamSandboxes))+1 > teamInfo.Limits.SandboxConcurrency:
		a.sendAPIStoreError(c, http.StatusTooManyRequests, fmt.Sprintf(
			"forking would exceed the maximum number of concurrent E2B sandboxes (%d). If you need more, "+
				"please visit 'https://e2b.dev/docs/billing'", teamInfo.Limits.SandboxConcurrency))

		return
	}

	pause.LogInitiated(ctx, sandboxID, teamID.String(), pause.ReasonFork)

	err = a.orchestrator.RemoveSandbox(ctx, teamID, sandboxID, sandbox.RemoveOpts{Action: sandbox.StateActionPause, FilesystemOnly: filesystemOnly})
	var transErr *sandbox.InvalidStateTransitionError

	switch {
	case err == nil:
		pause.LogSuccess(ctx, sandboxID, teamID.String(), pause.ReasonFork)
	case errors.Is(err, orchestrator.ErrSandboxNotFound):
		apiErr := pauseHandleNotRunningSandbox(ctx, a.snapshotCache, sandboxID, teamID)
		switch apiErr.Code {
		case http.StatusConflict:
			pause.LogSkipped(ctx, sandboxID, teamID.String(), pause.ReasonFork, pause.SkipReasonAlreadyPaused)
		case http.StatusNotFound:
			pause.LogSkipped(ctx, sandboxID, teamID.String(), pause.ReasonFork, pause.SkipReasonNotFound)
		default:
			pause.LogFailure(ctx, sandboxID, teamID.String(), pause.ReasonFork, err)
		}
		a.sendAPIStoreError(c, apiErr.Code, apiErr.ClientMsg)

		return
	case errors.As(err, &transErr):
		pause.LogFailure(ctx, sandboxID, teamID.String(), pause.ReasonFork, err)
		a.sendAPIStoreError(c, http.StatusConflict, fmt.Sprintf("Sandbox '%s' cannot be forked while in '%s' state", sandboxID, transErr.CurrentState))

		return
	default:
		pause.LogFailure(ctx, sandboxID, teamID.String(), pause.ReasonFork, err)
		telemetry.ReportError(ctx, "error pausing sandbox for fork", err)

		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error forking sandbox")

		return
	}

	forkedSandboxID := InstanceIDPrefix + id.Generate()

	sbxlogger.E(&sbxlogger.SandboxMetadata{
		SandboxID:  sandboxID,
		TemplateID: original.TemplateID,
		TeamID:     teamID.String(),
	}).Debug(ctx, "Resuming original sandbox and creating forked sandbox from snapshot")

	// Resume the original sandbox and create the forked sandbox concurrently:
	// both boot independently from the same immutable snapshot, so neither
	// depends on the other's completion.
	var (
		originalErr *api.APIError
		forkedSbx   *api.Sandbox
		forkedErr   *api.APIError
	)

	wg := errgroup.Group{}

	wg.Go(func() error {
		// Resume the original sandbox under its original ID so it keeps running.
		// Compute the remaining time to live only now, after the pause, so the
		// resumed sandbox keeps the expiration captured before pausing.
		originalTimeout := time.Until(originalEndTime)
		if originalTimeout <= 0 {
			originalTimeout = sandbox.SandboxTimeoutDefault
		}

		_, createErr := a.startSandbox(
			ctx,
			sandboxID,
			originalTimeout,
			teamInfo,
			a.buildResumeSandboxData(sandboxID, sandboxID, nil),
			&c.Request.Header,
			true,
			nil, // mcp
		)
		originalErr = createErr

		return nil
	})

	wg.Go(func() error {
		// Create the new forked sandbox from the same snapshot, under a new ID.
		sbx, createErr := a.startSandbox(
			ctx,
			forkedSandboxID,
			forkTimeout,
			teamInfo,
			a.buildResumeSandboxData(sandboxID, forkedSandboxID, nil),
			&c.Request.Header,
			true,
			nil, // mcp
		)
		forkedSbx = sbx
		forkedErr = createErr

		return nil
	})

	_ = wg.Wait()

	if originalErr != nil {
		telemetry.ReportError(ctx, "error resuming original sandbox after fork", originalErr.Err, telemetry.WithSandboxID(sandboxID))

		// The caller never learns the forked sandbox's ID, so it can't manage
		// it; clean it up rather than leaving it running unaccounted for.
		if forkedErr == nil {
			killErr := a.orchestrator.RemoveSandbox(context.WithoutCancel(ctx), teamID, forkedSandboxID, sandbox.RemoveOpts{Action: sandbox.StateActionKill, Reason: sandbox.KillReasonOrphaned})
			if killErr != nil {
				telemetry.ReportError(ctx, "error cleaning up forked sandbox after original resume failure", killErr, telemetry.WithSandboxID(forkedSandboxID))
			}
		}

		a.sendAPIStoreError(c, originalErr.Code, fmt.Sprintf(
			"Fork failed: the original sandbox was paused but could not be resumed; it can be restored with POST /sandboxes/%s/resume: %s",
			sandboxID, originalErr.ClientMsg))

		return
	}

	if forkedErr != nil {
		a.sendAPIStoreError(c, forkedErr.Code, forkedErr.ClientMsg)

		return
	}

	c.JSON(http.StatusCreated, forkedSbx)
}
