package api

import (
	"connectrpc.com/authn"
	"context"
	"encoding/json"
	"net/http"
	"slices"

	"github.com/rs/zerolog"

	"github.com/e2b-dev/infra/packages/envd/internal/host"
	"github.com/e2b-dev/infra/packages/envd/internal/utils"
)

var (
	accessTokenHeader = "X-Access-Token"

	// todo: include HTTP method here!
	alwaysAllowedProcedures = []string{"/health", "/files"}
)

type API struct {
	logger      *zerolog.Logger
	accessToken *string
	envVars     *utils.Map[string, string]
}

func New(l *zerolog.Logger, envVars *utils.Map[string, string]) *API {
	return &API{logger: l, envVars: envVars}
}

func (a *API) AuthenticateAccessToken(_ context.Context, req authn.Request) (any, error) {
	// access token is required and it's not procedure with auth exception
	if a.accessToken != nil && !slices.Contains(alwaysAllowedProcedures, req.Procedure()) {
		authHeader := req.Header().Get(accessTokenHeader)
		if authHeader != *a.accessToken {
			return nil, authn.Errorf("invalid access token")
		}
	}

	return nil, nil
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
	json.NewEncoder(w).Encode(metrics)
}
