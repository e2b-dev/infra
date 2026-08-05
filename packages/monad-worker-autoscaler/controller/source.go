package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type OverviewSource interface {
	Fetch(ctx context.Context) (Overview, error)
}

type FleetSource interface {
	Fetch(ctx context.Context) (Fleet, error)
}

type IdentityTokenSource interface {
	Token(ctx context.Context, audience string) (string, error)
}

type HTTPOverviewSource struct {
	URL         string
	Audience    string
	TokenSource IdentityTokenSource
	Client      *http.Client
}

func (s HTTPOverviewSource) Fetch(ctx context.Context) (Overview, error) {
	if s.Client == nil {
		return Overview{}, errors.New("TAMS capacity HTTP client is required")
	}
	if s.TokenSource == nil {
		return Overview{}, errors.New("TAMS identity token source is required")
	}
	if err := validateIdentityDestination(s.URL, s.Audience); err != nil {
		return Overview{}, err
	}
	token, err := s.TokenSource.Token(ctx, s.Audience)
	if err != nil {
		return Overview{}, fmt.Errorf("mint TAMS workload identity token: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.URL, nil)
	if err != nil {
		return Overview{}, fmt.Errorf("create TAMS capacity request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := s.Client.Do(req)
	if err != nil {
		return Overview{}, fmt.Errorf("fetch TAMS capacity: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))

		return Overview{}, fmt.Errorf("fetch TAMS capacity: unexpected HTTP %d", resp.StatusCode)
	}

	return DecodeOverview(resp.Body)
}

func validateIdentityDestination(endpointValue, audienceValue string) error {
	endpoint, err := url.Parse(endpointValue)
	if err != nil || endpoint.Host == "" {
		return errors.New("TAMS capacity URL must be an absolute HTTPS URL")
	}
	audience, err := url.Parse(audienceValue)
	if err != nil || audience.Host == "" {
		return errors.New("TAMS identity audience must be an absolute HTTPS URL")
	}
	if endpoint.Scheme != "https" || audience.Scheme != "https" {
		return errors.New("TAMS capacity URL and identity audience must use HTTPS")
	}
	if endpoint.User != nil || audience.User != nil {
		return errors.New("TAMS capacity URL and identity audience must not contain credentials")
	}
	if !strings.EqualFold(endpoint.Hostname(), audience.Hostname()) || effectiveHTTPSPort(endpoint) != effectiveHTTPSPort(audience) {
		return errors.New("TAMS capacity URL and identity audience must use the same origin")
	}

	return nil
}

func effectiveHTTPSPort(value *url.URL) string {
	if value.Port() != "" {
		return value.Port()
	}

	return "443"
}

const GoogleMetadataIdentityEndpoint = "http://metadata.google.internal/computeMetadata/v1/instance/service-accounts/default/identity"

type MetadataIdentityTokenSource struct {
	Endpoint string
	Client   *http.Client
}

func (s MetadataIdentityTokenSource) Token(ctx context.Context, audience string) (string, error) {
	if s.Client == nil {
		return "", errors.New("metadata identity HTTP client is required")
	}
	endpoint := s.Endpoint
	if endpoint == "" {
		endpoint = GoogleMetadataIdentityEndpoint
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return "", fmt.Errorf("parse metadata identity endpoint: %w", err)
	}
	query := parsed.Query()
	query.Set("audience", audience)
	query.Set("format", "full")
	parsed.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return "", fmt.Errorf("create metadata identity request: %w", err)
	}
	req.Header.Set("Metadata-Flavor", "Google")
	resp, err := s.Client.Do(req)
	if err != nil {
		return "", fmt.Errorf("mint attached-service-account identity token: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))

		return "", fmt.Errorf("mint attached-service-account identity token: unexpected HTTP %d", resp.StatusCode)
	}
	if resp.Header.Get("Metadata-Flavor") != "Google" {
		return "", errors.New("metadata identity response lacks Google provenance header")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, (64<<10)+1))
	if err != nil {
		return "", fmt.Errorf("read attached-service-account identity token: %w", err)
	}
	if len(body) > 64<<10 {
		return "", errors.New("attached-service-account identity token exceeds 64 KiB")
	}
	token := strings.TrimSpace(string(body))
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return "", errors.New("metadata identity endpoint returned a malformed token")
	}

	return token, nil
}

type NomadFleetSource struct {
	Address  string
	Token    string
	NodePool string
	Client   *http.Client
}

type nomadNode struct {
	ID                    string `json:"ID"`
	Name                  string `json:"Name"`
	Status                string `json:"Status"`
	SchedulingEligibility string `json:"SchedulingEligibility"`
	NodePool              string `json:"NodePool"`
}

func (s NomadFleetSource) Fetch(ctx context.Context) (Fleet, error) {
	if s.Client == nil {
		return Fleet{}, errors.New("nomad HTTP client is required")
	}
	base, err := url.Parse(s.Address)
	if err != nil {
		return Fleet{}, fmt.Errorf("parse Nomad address: %w", err)
	}
	base.Path = strings.TrimRight(base.Path, "/") + "/v1/nodes"
	query := base.Query()
	query.Set("filter", fmt.Sprintf(`NodePool == %q`, s.NodePool))
	base.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base.String(), nil)
	if err != nil {
		return Fleet{}, fmt.Errorf("create Nomad nodes request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Nomad-Token", s.Token)

	resp, err := s.Client.Do(req)
	if err != nil {
		return Fleet{}, fmt.Errorf("fetch Nomad worker nodes: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))

		return Fleet{}, fmt.Errorf("fetch Nomad worker nodes: unexpected HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, (4<<20)+1))
	if err != nil {
		return Fleet{}, fmt.Errorf("read Nomad worker nodes: %w", err)
	}
	if len(body) > 4<<20 {
		return Fleet{}, errors.New("nomad worker-node response exceeds 4 MiB")
	}
	var nodes []nomadNode
	if err := json.Unmarshal(body, &nodes); err != nil {
		return Fleet{}, fmt.Errorf("decode Nomad worker nodes: %w", err)
	}
	seenIDs := make(map[string]struct{}, len(nodes))
	seenNames := make(map[string]struct{}, len(nodes))
	fleet := Fleet{}
	for _, node := range nodes {
		if node.ID == "" || node.Name == "" {
			return Fleet{}, errors.New("nomad returned a worker node without ID or name")
		}
		if node.NodePool != s.NodePool {
			return Fleet{}, fmt.Errorf("nomad returned node %q from unexpected pool %q", node.Name, node.NodePool)
		}
		if _, exists := seenIDs[node.ID]; exists {
			return Fleet{}, fmt.Errorf("nomad returned duplicate worker node ID %q", node.ID)
		}
		if _, exists := seenNames[node.Name]; exists {
			return Fleet{}, fmt.Errorf("nomad returned duplicate worker node name %q", node.Name)
		}
		seenIDs[node.ID] = struct{}{}
		seenNames[node.Name] = struct{}{}

		if node.Status != "ready" {
			return Fleet{}, fmt.Errorf("nomad worker node %q has ambiguous status %q", node.Name, node.Status)
		}
		switch node.SchedulingEligibility {
		case "eligible":
		case "ineligible":
			fleet.DrainingHosts++
		default:
			return Fleet{}, fmt.Errorf("nomad worker node %q has unknown scheduling eligibility %q", node.Name, node.SchedulingEligibility)
		}
		fleet.ActualHosts++
	}

	return fleet, nil
}

func NewBoundedHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			MaxIdleConns:          10,
			IdleConnTimeout:       30 * time.Second,
			TLSHandshakeTimeout:   5 * time.Second,
			ResponseHeaderTimeout: 5 * time.Second,
		},
	}
}

func NewMetadataHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 5 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Transport: &http.Transport{
			Proxy:                 nil,
			MaxIdleConns:          2,
			IdleConnTimeout:       30 * time.Second,
			ResponseHeaderTimeout: 3 * time.Second,
		},
	}
}
