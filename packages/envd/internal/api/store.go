package api

import (
	"encoding/json"
	"github.com/e2b-dev/infra/packages/envd/internal/utils"
	"io"
	"net/http"

	"github.com/e2b-dev/infra/packages/envd/internal/host"
	"github.com/e2b-dev/infra/packages/envd/internal/logs"

	"github.com/rs/zerolog"
)

type API struct {
	logger  *zerolog.Logger
	envVars *utils.Map[string, string]
}

func New(l *zerolog.Logger, envVars *utils.Map[string, string]) *API {
	return &API{logger: l, envVars: envVars}
}

func (a *API) PostInit(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()

	if r.Body != nil {
		var initRequest PostInitJSONBody

		err := json.NewDecoder(r.Body).Decode(&initRequest)
		if err != nil && err != io.EOF {
			a.logger.Error().Msgf("Failed to decode request: %v", err)
			w.WriteHeader(http.StatusBadRequest)

			return
		}

		if initRequest.EnvVars != nil {
			for key, value := range *initRequest.EnvVars {
				println("key", key, "value", value)
				a.envVars.Store(key, value)
			}
		}
	}

	operationID := logs.AssignOperationID()

	a.logger.Debug().Str(string(logs.OperationIDKey), operationID).Msg("Syncing host")

	go func() {
		err := host.Sync()
		if err != nil {
			a.logger.Error().Str(string(logs.OperationIDKey), operationID).Msgf("Failed to sync clock: %v", err)
		} else {
			a.logger.Trace().Str(string(logs.OperationIDKey), operationID).Msg("Clock synced")
		}
	}()

	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "")

	w.WriteHeader(http.StatusNoContent)
}

func (a *API) GetHealth(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()

	a.logger.Debug().Msg("Health check")

	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "")

	w.WriteHeader(http.StatusNoContent)
}
