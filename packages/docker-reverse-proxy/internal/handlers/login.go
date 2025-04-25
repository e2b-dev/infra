package handlers

import (
	"log"
	"net/http"
	"strings"

	"github.com/e2b-dev/infra/packages/docker-reverse-proxy/internal/utils"
)

func (a *APIStore) LoginWithToken(w http.ResponseWriter, r *http.Request) error {
	// Validate the token by checking if the generated token is in the cache
	authHeader := r.Header.Get("Authorization")
	e2bToken := strings.TrimPrefix(authHeader, "Bearer ")
	_, err := a.AuthCache.Get(e2bToken)
	if err != nil {
		log.Printf("Error while logging with access token: %s, header: %s\n", err, authHeader)
		utils.SetDockerUnauthorizedHeaders(w)

		return err
	}

	return nil
}
