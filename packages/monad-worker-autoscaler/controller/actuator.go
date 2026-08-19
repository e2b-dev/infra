package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Actuator is the one mutation seam the controller has. It only ever grows
// the worker group: the scale-in path stays decision-only until a typed
// drain owner exists on both sides of the capacity contract.
type Actuator interface {
	TargetSize(ctx context.Context) (int, error)
	Resize(ctx context.Context, target int) error
}

type AccessTokenSource interface {
	AccessToken(ctx context.Context) (string, error)
}

const GoogleMetadataAccessTokenEndpoint = "http://metadata.google.internal/computeMetadata/v1/instance/service-accounts/default/token"

// accessTokenExpiryMargin refreshes ahead of expiry so a token never goes
// stale between the cache check and the Compute API call.
const accessTokenExpiryMargin = 60 * time.Second

type MetadataAccessTokenSource struct {
	Endpoint string
	Client   *http.Client
	Now      func() time.Time

	mu        sync.Mutex
	token     string
	expiresAt time.Time
}

func (s *MetadataAccessTokenSource) AccessToken(ctx context.Context) (string, error) {
	if s.Client == nil {
		return "", errors.New("metadata access-token HTTP client is required")
	}
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.token != "" && now().Before(s.expiresAt.Add(-accessTokenExpiryMargin)) {
		return s.token, nil
	}

	endpoint := s.Endpoint
	if endpoint == "" {
		endpoint = GoogleMetadataAccessTokenEndpoint
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", fmt.Errorf("create metadata access-token request: %w", err)
	}
	req.Header.Set("Metadata-Flavor", "Google")
	resp, err := s.Client.Do(req)
	if err != nil {
		return "", fmt.Errorf("mint attached-service-account access token: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))

		return "", fmt.Errorf("mint attached-service-account access token: unexpected HTTP %d", resp.StatusCode)
	}
	if resp.Header.Get("Metadata-Flavor") != "Google" {
		return "", errors.New("metadata access-token response lacks Google provenance header")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, (64<<10)+1))
	if err != nil {
		return "", fmt.Errorf("read attached-service-account access token: %w", err)
	}
	if len(body) > 64<<10 {
		return "", errors.New("attached-service-account access token exceeds 64 KiB")
	}
	var payload struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		TokenType   string `json:"token_type"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", fmt.Errorf("decode attached-service-account access token: %w", err)
	}
	if payload.AccessToken == "" || payload.TokenType != "Bearer" || payload.ExpiresIn <= 0 {
		return "", errors.New("metadata endpoint returned a malformed access token")
	}
	s.token = payload.AccessToken
	s.expiresAt = now().Add(time.Duration(payload.ExpiresIn) * time.Second)

	return s.token, nil
}

var (
	gceProjectPattern  = regexp.MustCompile(`^[a-z][-a-z0-9]{4,28}[a-z0-9]$`)
	gceRegionPattern   = regexp.MustCompile(`^[a-z]+-[a-z0-9]+\d$`)
	gceResourcePattern = regexp.MustCompile(`^[a-z](?:[-a-z0-9]{0,61}[a-z0-9])?$`)
)

type GCERegionMIGActuator struct {
	client   *http.Client
	tokens   AccessTokenSource
	endpoint string
}

func NewGCERegionMIGActuator(client *http.Client, tokens AccessTokenSource, baseURL, project, region, name string) (*GCERegionMIGActuator, error) {
	if client == nil {
		return nil, errors.New("compute HTTP client is required")
	}
	if tokens == nil {
		return nil, errors.New("compute access-token source is required")
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Host == "" {
		return nil, errors.New("compute API base must be an absolute HTTPS URL")
	}
	if parsed.User != nil {
		return nil, errors.New("compute API base must not contain URL credentials")
	}
	loopback := parsed.Hostname() == "127.0.0.1" || parsed.Hostname() == "localhost" || parsed.Hostname() == "::1"
	if parsed.Scheme != "https" && !(parsed.Scheme == "http" && loopback) {
		return nil, errors.New("compute API base may use cleartext HTTP only on loopback")
	}
	if !gceProjectPattern.MatchString(project) {
		return nil, fmt.Errorf("invalid worker group project %q", project)
	}
	if !gceRegionPattern.MatchString(region) {
		return nil, fmt.Errorf("invalid worker group region %q", region)
	}
	if !gceResourcePattern.MatchString(name) {
		return nil, fmt.Errorf("invalid worker group name %q", name)
	}

	return &GCERegionMIGActuator{
		client: client,
		tokens: tokens,
		endpoint: fmt.Sprintf(
			"%s/compute/v1/projects/%s/regions/%s/instanceGroupManagers/%s",
			strings.TrimRight(baseURL, "/"), project, region, name,
		),
	}, nil
}

func (a *GCERegionMIGActuator) TargetSize(ctx context.Context) (int, error) {
	body, err := a.call(ctx, http.MethodGet, a.endpoint)
	if err != nil {
		return 0, fmt.Errorf("read worker group: %w", err)
	}
	var payload struct {
		TargetSize *int `json:"targetSize"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return 0, fmt.Errorf("decode worker group: %w", err)
	}
	if payload.TargetSize == nil || *payload.TargetSize < 0 {
		return 0, errors.New("worker group reported no usable target size")
	}

	return *payload.TargetSize, nil
}

func (a *GCERegionMIGActuator) Resize(ctx context.Context, target int) error {
	// The mutator owns the policy bounds; this is a last-resort envelope so a
	// defect upstream can never turn into an out-of-envelope fleet mutation.
	if target < 1 || target > MaximumWorkerHosts {
		return fmt.Errorf("refusing worker group resize to out-of-envelope target %d", target)
	}
	if _, err := a.call(ctx, http.MethodPost, fmt.Sprintf("%s/resize?size=%d", a.endpoint, target)); err != nil {
		return fmt.Errorf("resize worker group to %d: %w", target, err)
	}

	return nil
}

func (a *GCERegionMIGActuator) call(ctx context.Context, method, endpoint string) ([]byte, error) {
	token, err := a.tokens.AccessToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("mint compute access token: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create compute request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := a.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call compute API: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))

		return nil, fmt.Errorf("compute API returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil {
		return nil, fmt.Errorf("read compute response: %w", err)
	}
	if len(body) > 1<<20 {
		return nil, errors.New("compute response exceeds 1 MiB")
	}

	return body, nil
}
