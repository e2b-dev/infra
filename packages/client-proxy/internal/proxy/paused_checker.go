package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	proxygrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/proxy"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

type PausedSandboxChecker interface {
	PausedInfo(ctx context.Context, sandboxId string) (PausedInfo, error)
	Resume(ctx context.Context, sandboxId string, timeoutSeconds int32) error
}

type apiPausedSandboxChecker struct {
	baseURL           string
	adminToken        string
	apiKey            string
	autoResumeEnabled bool
	pausedClient      *http.Client
	resumeClient      *http.Client
}

type PausedInfo struct {
	Paused           bool
	AutoResumePolicy proxygrpc.AutoResumePolicy
}

func NewApiPausedSandboxChecker(baseURL, adminToken, apiKey string, autoResumeEnabled bool) (PausedSandboxChecker, error) {
	if strings.TrimSpace(baseURL) == "" || (strings.TrimSpace(adminToken) == "" && strings.TrimSpace(apiKey) == "") {
		return nil, nil
	}

	_, err := url.ParseRequestURI(baseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid API base URL: %w", err)
	}

	return &apiPausedSandboxChecker{
		baseURL:           strings.TrimRight(baseURL, "/"),
		adminToken:        adminToken,
		apiKey:            apiKey,
		autoResumeEnabled: autoResumeEnabled,
		pausedClient: &http.Client{
			Timeout: 5 * time.Second,
		},
		resumeClient: &http.Client{
			Timeout: 35 * time.Second,
		},
	}, nil
}

type sandboxStateResponse struct {
	State    string            `json:"state"`
	Metadata map[string]string `json:"metadata"`
}

func (c *apiPausedSandboxChecker) PausedInfo(ctx context.Context, sandboxId string) (PausedInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/sandboxes/"+sandboxId, nil)
	if err != nil {
		return PausedInfo{}, fmt.Errorf("create request: %w", err)
	}

	if strings.TrimSpace(c.apiKey) != "" {
		req.Header.Set("X-API-Key", c.apiKey)
	} else {
		req.Header.Set("X-Admin-Token", c.adminToken)
	}

	resp, err := c.pausedClient.Do(req)
	if err != nil {
		return PausedInfo{}, fmt.Errorf("request failed: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	switch resp.StatusCode {
	case http.StatusOK:
		var payload sandboxStateResponse
		if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
			return PausedInfo{}, fmt.Errorf("decode response: %w", err)
		}
		paused := strings.EqualFold(payload.State, "paused")

		return PausedInfo{
			Paused:           paused,
			AutoResumePolicy: getAutoResumePolicy(payload.Metadata),
		}, nil
	case http.StatusNotFound:
		return PausedInfo{Paused: false, AutoResumePolicy: proxygrpc.AutoResumePolicy_AUTO_RESUME_POLICY_NULL}, nil
	case http.StatusUnauthorized, http.StatusForbidden:
		return PausedInfo{}, errors.New("api auth failed for paused lookup")
	default:
		return PausedInfo{}, fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}
}

type resumeRequest struct {
	Timeout int32 `json:"timeout,omitempty"`
}

func (c *apiPausedSandboxChecker) Resume(ctx context.Context, sandboxId string, timeoutSeconds int32) error {
	reqBody := resumeRequest{Timeout: timeoutSeconds}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("marshal resume body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/sandboxes/"+sandboxId+"/resume", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create resume request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if strings.TrimSpace(c.apiKey) != "" {
		req.Header.Set("X-API-Key", c.apiKey)
	} else {
		req.Header.Set("X-Admin-Token", c.adminToken)
	}

	resp, err := c.resumeClient.Do(req)
	if err != nil {
		return fmt.Errorf("resume request failed: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	switch resp.StatusCode {
	case http.StatusCreated, http.StatusOK, http.StatusConflict:
		return nil
	case http.StatusNotFound:
		return fmt.Errorf("resume failed: sandbox not found")
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("resume failed: api auth error")
	default:
		return fmt.Errorf("resume failed: unexpected status %d", resp.StatusCode)
	}
}

func logSleeping(ctx context.Context, sandboxId string) {
	logger.L().Info(ctx, "im sleeping", logger.WithSandboxID(sandboxId))
}

func getAutoResumePolicy(metadata map[string]string) proxygrpc.AutoResumePolicy {
	return proxygrpc.AutoResumePolicyFromMetadata(metadata)
}
