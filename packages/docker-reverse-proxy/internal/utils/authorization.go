package utils

import (
	"fmt"
	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	"net/http"
)

// SetDockerUnauthorizedHeaders https://distribution.github.io/distribution/spec/api/#api-version-check
func SetDockerUnauthorizedHeaders(w http.ResponseWriter) {
	// Set the WWW-Authenticate header to indicate the next action
	w.Header().Set("Www-Authenticate", fmt.Sprintf("Bearer realm=\"https://docker.%s/v2/token\"", consts.Domain))
	// Required for docker registry v2
	w.Header().Set("Docker-Distribution-API-Version", "registry/2.0")
	w.WriteHeader(http.StatusUnauthorized)
}
