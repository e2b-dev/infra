package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

type PausedSandboxChecker interface {
	IsPaused(ctx context.Context, sandboxId string) (bool, error)
}

type apiPausedSandboxChecker struct {
	baseURL    string
	adminToken string
	apiKey     string
	client     *http.Client
}

func NewApiPausedSandboxChecker(baseURL, adminToken, apiKey string) (PausedSandboxChecker, error) {
	if strings.TrimSpace(baseURL) == "" || (strings.TrimSpace(adminToken) == "" && strings.TrimSpace(apiKey) == "") {
		return nil, nil
	}

	_, err := url.ParseRequestURI(baseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid API base URL: %w", err)
	}

	return &apiPausedSandboxChecker{
		baseURL:    strings.TrimRight(baseURL, "/"),
		adminToken: adminToken,
		apiKey:     apiKey,
		client: &http.Client{
			Timeout: 2 * time.Second,
		},
	}, nil
}

type sandboxStateResponse struct {
	State string `json:"state"`
}

func (c *apiPausedSandboxChecker) IsPaused(ctx context.Context, sandboxId string) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/sandboxes/"+sandboxId, nil)
	if err != nil {
		return false, fmt.Errorf("create request: %w", err)
	}

	if strings.TrimSpace(c.apiKey) != "" {
		req.Header.Set("X-API-Key", c.apiKey)
	} else {
		req.Header.Set("X-Admin-Token", c.adminToken)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return false, fmt.Errorf("request failed: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	switch resp.StatusCode {
	case http.StatusOK:
		var payload sandboxStateResponse
		if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
			return false, fmt.Errorf("decode response: %w", err)
		}
		return strings.EqualFold(payload.State, "paused"), nil
	case http.StatusNotFound:
		return false, nil
	case http.StatusUnauthorized, http.StatusForbidden:
		return false, errors.New("api auth failed for paused lookup")
	default:
		return false, fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}
}

func logSleeping(ctx context.Context, sandboxId string) {
	logger.L().Info(ctx, "im sleeping", logger.WithSandboxID(sandboxId))
}
