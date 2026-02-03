package orchestrator

import (
	"context"
	"errors"
	"maps"
	"net/http"

	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
)

var errSandboxNotRunning = errors.New("sandbox not running")

func (o *Orchestrator) UpdateSandboxMetadata(ctx context.Context, teamID uuid.UUID, sandboxID string, updates map[string]string) *api.APIError {
	updateFunc := func(sbx sandbox.Sandbox) (sandbox.Sandbox, error) {
		if sbx.State != sandbox.StateRunning {
			return sbx, errSandboxNotRunning
		}

		if updates == nil {
			return sbx, nil
		}

		merged := make(map[string]string, len(sbx.Metadata)+len(updates))
		maps.Copy(merged, sbx.Metadata)
		maps.Copy(merged, updates)

		sbx.Metadata = merged

		return sbx, nil
	}

	_, err := o.sandboxStore.Update(ctx, teamID, sandboxID, updateFunc)
	if err != nil {
		var sbxNotFoundErr *sandbox.NotFoundError
		switch {
		case errors.Is(err, errSandboxNotRunning):
			return &api.APIError{Code: http.StatusConflict, ClientMsg: "Sandbox must be running to update metadata", Err: err}
		case errors.As(err, &sbxNotFoundErr):
			return &api.APIError{Code: http.StatusNotFound, ClientMsg: "Sandbox not found", Err: err}
		default:
			return &api.APIError{Code: http.StatusInternalServerError, ClientMsg: "Error when updating sandbox metadata", Err: err}
		}
	}

	return nil
}
