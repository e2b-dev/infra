package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"

	"github.com/e2b-dev/infra/packages/envd/internal/host"
	"github.com/e2b-dev/infra/packages/envd/internal/utils"
	"github.com/e2b-dev/infra/packages/shared/pkg/keys"
	"github.com/rs/zerolog"
)

const (
	SigningReadOperation  = "read"
	SigningWriteOperation = "write"
)

var (
	accessTokenHeader = "X-Access-Token"

	// paths that are always allowed without general authentication
	allowedPaths = []string{
		"GET/health",
		"GET/files",
		"POST/files",
	}
)

type API struct {
	logger      *zerolog.Logger
	accessToken *string
	envVars     *utils.Map[string, string]
}

type jsonErrorResponse struct {
	Error string `json:"error"`
}

func New(l *zerolog.Logger, envVars *utils.Map[string, string]) *API {
	return &API{logger: l, envVars: envVars}
}

func (a *API) WithAuthorization(handler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if a.accessToken != nil {
			authHeader := req.Header.Get(accessTokenHeader)

			// check if this path is allowed without authentication (e.g., health check, endpoints supporting signing)
			allowedPath := slices.Contains(allowedPaths, req.Method+req.URL.Path)

			if authHeader != *a.accessToken && !allowedPath {
				response := jsonErrorResponse{Error: "Unauthorized access, please provide a valid access token or method signing if supported"}
				responseBytes, _ := json.Marshal(response)

				a.logger.Error().Msg("Trying to access secured envd without correct access token")

				w.WriteHeader(http.StatusUnauthorized)
				w.Header().Add("Content-Type", "application/json")
				w.Write(responseBytes)
				return
			}
		}

		handler.ServeHTTP(w, req)
	})
}

func (a *API) generateSigning(path string, username string, operation string) (string, error) {
	if a.accessToken == nil {
		return "", fmt.Errorf("access token is not set")
	}

	hasher := keys.NewSHA256Hashing()
	signing := fmt.Sprintf("%s:%s:%s:%s", path, operation, username, *a.accessToken)
	signingWithVersionPrefix := fmt.Sprintf("v1_%s", hasher.Hash([]byte(signing)))

	return signingWithVersionPrefix, nil
}

func (a *API) validateSigning(w http.ResponseWriter, r *http.Request, signing *string, username string, path string, operation string) (ok bool, err error) {
	var errMsg error
	var errorCode int

	// no need to validate signing key if access token is not set
	if a.accessToken == nil {
		return true, nil
	}

	// check if access token is sent in the header
	tokenFromHeader := r.Header.Get(accessTokenHeader)
	if tokenFromHeader != "" && tokenFromHeader != *a.accessToken {
		errMsg = fmt.Errorf("access token present in header but does not match")
		errorCode = http.StatusUnauthorized

		a.logger.Err(errMsg)
		jsonError(w, errorCode, errMsg)
		return false, errMsg
	}

	if signing == nil {
		errMsg = fmt.Errorf("missing signing key")
		errorCode = http.StatusUnauthorized

		a.logger.Err(errMsg)
		jsonError(w, errorCode, errMsg)
		return false, errMsg
	}

	hash, err := a.generateSigning(path, username, operation)
	if err != nil {
		errorCode = http.StatusInternalServerError
		errMsg = fmt.Errorf("error building signing key for user '%s' and path '%s'", username, path)

		a.logger.Err(errMsg)
		jsonError(w, errorCode, errMsg)
		return false, errMsg
	}

	if hash != *signing {
		errMsg = fmt.Errorf("bad signing key for user '%s' and path '%s'", username, path)
		errorCode = http.StatusUnauthorized

		a.logger.Err(errMsg)
		jsonError(w, errorCode, errMsg)
		return false, errMsg
	}

	return true, nil
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
