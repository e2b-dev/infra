package api

import (
	"encoding/json"
	"net/http"

	"github.com/e2b-dev/infra/packages/envd/internal/logs"
)

func (a *API) GetEnvs(w http.ResponseWriter, _ *http.Request) {
	operationID := logs.AssignOperationID()

	a.logger.Debug().Str(string(logs.OperationIDKey), operationID).Msg("Getting env vars")

	envs := make(EnvVars)
	a.defaults.EnvVars.Range(func(key, value string) bool {
		envs[key] = value

		return true
	})

	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")

	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(envs); err != nil {
		a.logger.Error().Err(err).Str(string(logs.OperationIDKey), operationID).Msg("Failed to encode env vars")
	}
}
