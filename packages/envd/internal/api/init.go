package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/e2b-dev/infra/packages/envd/internal/host"
	"github.com/e2b-dev/infra/packages/envd/internal/logs"
)

func (a *API) PostInit(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()

	operationID := logs.AssignOperationID()

	if r.Body != nil {
		var initRequest PostInitJSONBody

		err := json.NewDecoder(r.Body).Decode(&initRequest)
		if err != nil && err != io.EOF {
			a.logger.Error().Str(string(logs.OperationIDKey), operationID).Msgf("Failed to decode request: %v", err)
			w.WriteHeader(http.StatusBadRequest)

			return
		}

		if initRequest.EnvVars != nil {
			a.logger.Debug().Str(string(logs.OperationIDKey), operationID).Msg(fmt.Sprintf("Setting %d env vars", len(*initRequest.EnvVars)))

			for key, value := range *initRequest.EnvVars {
				a.logger.Debug().Str(string(logs.OperationIDKey), operationID).Msgf("Setting env var for %s", key)

				a.envVars.Store(key, value)
			}
		}

		if initRequest.AccessToken != nil {
			a.logger.Debug().Str(string(logs.OperationIDKey), operationID).Msg("Setting access token")
			a.accessToken = initRequest.AccessToken
		}
	}

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
