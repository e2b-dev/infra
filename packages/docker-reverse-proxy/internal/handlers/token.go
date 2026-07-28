package handlers

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/e2b-dev/infra/packages/docker-reverse-proxy/internal/auth"
	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
)

// The scope is in format "repository:<project>/<repo>/<templateID>:<action>"
var scopeRegex = regexp.MustCompile(`^repository:e2b/custom-envs/(?P<templateID>[^:]+):(?P<action>[^:]+)$`)

// GetToken validates if user has access to template and then returns a new token for the required scope
func (a *APIStore) GetToken(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()

	// To get the token, the docker CLI uses Basic Auth in format "username:password",
	// where username should be "_e2b_access_token" and password is the actual access token
	authHeader := r.Header.Get("Authorization")

	accessToken, err := auth.ExtractAccessToken(authHeader, "Basic ")
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)

		return fmt.Errorf("error while extracting access token: %w", err)
	}

	userID, ok := auth.ValidateAccessToken(ctx, a.authDb, accessToken)
	if !ok {
		log.Printf("Invalid access token: '%s'\n", accessToken)

		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte("invalid access token"))

		return errors.New("invalid access token")
	}

	// Access token acceptance is gated after validation so the flag can be
	// rolled out per-user via LD targeting during the deprecation cutover.
	if a.featureFlags.BoolFlag(ctx, featureflags.DisableE2BAccessTokenAuthFlag, featureflags.UserContext(userID.String())) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte("E2B_ACCESS_TOKEN is deprecated and no longer accepted. Use an API key (E2B_API_KEY) instead. See https://e2b.dev/docs/migration/access-token-deprecation"))

		return errors.New("access token authentication is disabled")
	}

	scope := r.URL.Query().Get("scope")
	hasScope := scope != ""

	if !hasScope {
		// If the scope is not provided, create a new token for the user,
		// but don't grant any access to the underlying repository.
		jsonResponse := a.AuthCache.Create("not-yet-known", int(time.Hour.Seconds()))

		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(jsonResponse))

		return nil
	}

	scopeRegexMatches := scopeRegex.FindStringSubmatch(scope)
	if len(scopeRegexMatches) == 0 {
		w.WriteHeader(http.StatusBadRequest)

		return fmt.Errorf("invalid scope %s", scope)
	}

	templateID := scopeRegexMatches[1]
	action := scopeRegexMatches[2]

	// Don't allow a delete actions
	if strings.Contains(action, "delete") {
		w.WriteHeader(http.StatusForbidden)

		return fmt.Errorf("access denied for scope %s", scope)
	}

	// Validate if the user has access to the template
	hasAccess, err := auth.Validate(ctx, a.db, accessToken, templateID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)

		return fmt.Errorf("error while validating access: %w", err)
	}

	if !hasAccess {
		w.WriteHeader(http.StatusForbidden)

		return fmt.Errorf("access denied for env: %s", templateID)
	}

	jsonResponse := a.AuthCache.Create(templateID, int(time.Hour.Seconds()))

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(jsonResponse))

	return nil
}
