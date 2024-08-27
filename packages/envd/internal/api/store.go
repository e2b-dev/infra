package api

import (
	"net/http"

	"github.com/rs/zerolog"

	"github.com/e2b-dev/infra/packages/envd/internal/utils"
)

type API struct {
	logger  *zerolog.Logger
	envVars *utils.Map[string, string]
}

func New(l *zerolog.Logger, envVars *utils.Map[string, string]) *API {
	return &API{logger: l, envVars: envVars}
}

func (a *API) GetHealth(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()

	a.logger.Debug().Msg("Health check")

	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "")

	w.WriteHeader(http.StatusNoContent)
}
