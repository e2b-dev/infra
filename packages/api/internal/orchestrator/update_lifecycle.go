package orchestrator

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
)

func (o *Orchestrator) UpdateSandboxLifecycle(ctx context.Context, teamID uuid.UUID, sandboxID string, autoPause bool) *api.APIError {
	updateFunc := func(sbx sandbox.Sandbox) (sandbox.Sandbox, error) {
		if sbx.State != sandbox.StateRunning {
			return sbx, errSandboxNotRunning
		}

		sbx.AutoPause = autoPause

		return sbx, nil
	}

	_, err := o.sandboxStore.Update(ctx, teamID, sandboxID, updateFunc)
	if err != nil {
		var sbxNotFoundErr *sandbox.NotFoundError
		switch {
		case errors.Is(err, errSandboxNotRunning):
			return &api.APIError{Code: http.StatusConflict, ClientMsg: "Sandbox must be running to update lifecycle", Err: err}
		case errors.As(err, &sbxNotFoundErr):
			return &api.APIError{Code: http.StatusNotFound, ClientMsg: "Sandbox not found", Err: err}
		default:
			return &api.APIError{Code: http.StatusInternalServerError, ClientMsg: "Error when updating sandbox lifecycle", Err: err}
		}
	}

	return nil
}
