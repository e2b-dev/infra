package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/e2b-dev/infra/packages/envd/internal/host"
	"github.com/e2b-dev/infra/packages/envd/internal/logs"
)

func (a *API) PostInit(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()

	operationID := logs.AssignOperationID()
	logger := a.logger.With().Str(string(logs.OperationIDKey), operationID).Logger()

	if r.Body != nil {
		var initRequest PostInitJSONBody

		err := json.NewDecoder(r.Body).Decode(&initRequest)
		if err != nil && err != io.EOF {
			logger.Error().Msgf("Failed to decode request: %v", err)
			w.WriteHeader(http.StatusBadRequest)

			return
		}

		if initRequest.EnvVars != nil {
			logger.Debug().Msg(fmt.Sprintf("Setting %d env vars", len(*initRequest.EnvVars)))

			for key, value := range *initRequest.EnvVars {
				logger.Debug().Msgf("Setting env var for %s", key)
				a.envVars.Store(key, value)
			}
		}

		if initRequest.AccessToken != nil {
			if a.accessToken != nil && *initRequest.AccessToken != *a.accessToken {
				logger.Error().Msg("Access token is already set and cannot be changed")
				w.WriteHeader(http.StatusConflict)
				return
			}

			logger.Debug().Msg("Setting access token")
			a.accessToken = initRequest.AccessToken
		}
	}

	logger.Debug().Msg("Syncing host")

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		host.PollForMMDSOpts(ctx, a.mmdsChan, a.envVars)
	}()

	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "")

	w.WriteHeader(http.StatusNoContent)
}
