package api

import (
	"encoding/json"
	"net/http"

	"github.com/rs/zerolog"

	"github.com/e2b-dev/infra/packages/envd/internal/host"
	"github.com/e2b-dev/infra/packages/envd/internal/utils"
)

type API struct {
	isNotFC     bool
	logger      *zerolog.Logger
	accessToken *string
	envVars     *utils.Map[string, string]
	mmdsChan    chan *host.MMDSOpts
}

func New(l *zerolog.Logger, envVars *utils.Map[string, string], mmdsChan chan *host.MMDSOpts, isNotFC bool) *API {
	return &API{logger: l, envVars: envVars, mmdsChan: mmdsChan, isNotFC: isNotFC}
}

func (a *API) GetHealth(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()

	a.logger.Trace().Msg("Health check")

	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "")

	w.WriteHeader(http.StatusNoContent)
}

func (a *API) GetMetrics(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()

	a.logger.Trace().Msg("Get metrics")

	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")

	metrics, err := host.GetMetrics()
	if err != nil {
		a.logger.Error().Err(err).Msg("Failed to get metrics")
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	if err = json.NewEncoder(w).Encode(metrics); err != nil {
		a.logger.Error().Err(err).Msg("Failed to encode metrics")
	}
}
