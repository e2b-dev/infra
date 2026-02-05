package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/e2b-dev/infra/packages/docker-reverse-proxy/internal/auth"
	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
)

type DockerToken struct {
	Token     string `json:"token"`
	ExpiresIn int    `json:"expires_in"`
}

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

	if !auth.ValidateAccessToken(ctx, a.authDb, accessToken) {
		log.Printf("Invalid access token: '%s'\n", accessToken)

		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte("invalid access token"))

		return fmt.Errorf("invalid access token")
	}

	scope := r.URL.Query().Get("scope")
	hasScope := scope != ""

	if !hasScope {
		// If the scope is not provided, create a new token for the user,
		// but don't grant any access to the underlying repository.
		jsonResponse := a.AuthCache.Create("not-yet-known", "undefined-docker-token", int(time.Hour.Seconds()))

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

	// Get docker token from the actual registry
	dockerToken, err := getToken(ctx, templateID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)

		return fmt.Errorf("error while getting docker token: %w", err)
	}

	jsonResponse := a.AuthCache.Create(templateID, dockerToken.Token, dockerToken.ExpiresIn)

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(jsonResponse))

	return nil
}

// getToken gets a new token from the actual registry for the required scope
func getToken(ctx context.Context, templateID string) (*DockerToken, error) {
	scope := fmt.Sprintf(
		"?service=%s-docker.pkg.dev&scope=repository:%s/%s/%s:push,pull",
		consts.GCPRegion,
		consts.GCPProject,
		consts.DockerRegistry,
		templateID,
	)
	url := fmt.Sprintf(
		"https://%s-docker.pkg.dev/v2/token%s",
		consts.GCPRegion,
		scope,
	)

	r, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request for scope - %s: %w", templateID, err)
	}

	// Use the service account credentials for the request
	r.Header.Set("Authorization", fmt.Sprintf("Basic %s", consts.EncodedDockerCredentials))

	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		return nil, fmt.Errorf("failed to get token for scope - %s: %w", templateID, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body := make([]byte, resp.ContentLength)
		_, err := resp.Body.Read(body)
		if err != nil {
			return nil, fmt.Errorf("failed to read body for failed token acquisition (%d) for scope - %s: %w", resp.StatusCode, templateID, err)
		}
		defer resp.Body.Close()

		return nil, fmt.Errorf("failed to get token (%d) for scope - %s: %s", resp.StatusCode, templateID, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read body for successful token acquisition for scope - %s: %w", templateID, err)
	}

	parsedBody := &DockerToken{}
	err = json.Unmarshal(body, parsedBody)
	if err != nil {
		return nil, fmt.Errorf("failed to parse body for successful token acquisition for scope - %s: %w", templateID, err)
	}

	return parsedBody, nil
}
