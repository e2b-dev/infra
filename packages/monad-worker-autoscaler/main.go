package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/e2b-dev/infra/packages/monad-worker-autoscaler/consulleader"
	"github.com/e2b-dev/infra/packages/monad-worker-autoscaler/controller"
)

type config struct {
	TAMSURL      string
	TAMSAudience string
	NomadAddress string
	NomadToken   string
	NomadPool    string
	ConsulAddr   string
	ConsulToken  string
	ConsulKey    string
	InstanceID   string
	MetricsAddr  string
	Interval     time.Duration
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg, err := loadConfig()
	if err != nil {
		logger.Error("invalid configuration", "error", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	httpClient := controller.NewBoundedHTTPClient()
	metadataTokenSource := controller.MetadataIdentityTokenSource{Client: controller.NewMetadataHTTPClient()}
	metrics := &controller.Metrics{}
	server := &http.Server{
		Addr:              cfg.MetricsAddr,
		Handler:           metrics.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       30 * time.Second,
	}
	go func() {
		logger.Info("shadow controller metrics listening", "address", cfg.MetricsAddr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("metrics server failed", "error", err)
			stop()
		}
	}()

	runner := controller.Runner{
		Overview: controller.HTTPOverviewSource{URL: cfg.TAMSURL, Audience: cfg.TAMSAudience, TokenSource: metadataTokenSource, Client: httpClient},
		Fleet:    controller.NomadFleetSource{Address: cfg.NomadAddress, Token: cfg.NomadToken, NodePool: cfg.NomadPool, Client: httpClient},
		Leadership: &consulleader.Elector{
			Address: cfg.ConsulAddr, Token: cfg.ConsulToken, LockKey: cfg.ConsulKey,
			InstanceID: cfg.InstanceID, TTL: max(30*time.Second, cfg.Interval*4), Client: httpClient,
		},
		Engine: &controller.Engine{}, Metrics: metrics, Logger: logger, Interval: cfg.Interval,
	}
	if err := runner.Run(ctx); err != nil {
		logger.Error("controller stopped with error", "error", err)
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("metrics server shutdown failed", "error", err)
	}
}

func loadConfig() (config, error) {
	if mode := strings.TrimSpace(os.Getenv("MONAD_WORKER_AUTOSCALER_MODE")); mode != "shadow" {
		return config{}, fmt.Errorf("MONAD_WORKER_AUTOSCALER_MODE must be exactly shadow, got %q", mode)
	}
	if mutation := strings.TrimSpace(os.Getenv("MONAD_WORKER_AUTOSCALER_MUTATION_ENABLED")); mutation != "" && mutation != "false" && mutation != "0" {
		return config{}, errors.New("worker mutation is not implemented and must remain disabled")
	}
	cfg := config{
		TAMSURL:      strings.TrimSpace(os.Getenv("TAMS_OPS_CAPACITY_URL")),
		TAMSAudience: strings.TrimSpace(os.Getenv("TAMS_OPS_AUDIENCE")),
		NomadAddress: strings.TrimSpace(os.Getenv("NOMAD_ADDR")),
		NomadToken:   strings.TrimSpace(os.Getenv("NOMAD_TOKEN")),
		NomadPool:    envOrDefault("NOMAD_NODE_POOL", "default"),
		ConsulAddr:   strings.TrimSpace(os.Getenv("CONSUL_HTTP_ADDR")),
		ConsulToken:  strings.TrimSpace(os.Getenv("CONSUL_HTTP_TOKEN")),
		ConsulKey:    envOrDefault("CONSUL_LOCK_KEY", "service/monad-worker-autoscaler/leader"),
		InstanceID:   strings.TrimSpace(os.Getenv("CONTROLLER_INSTANCE_ID")),
		MetricsAddr:  envOrDefault("METRICS_ADDR", "127.0.0.1:9464"),
	}
	if cfg.InstanceID == "" {
		hostname, _ := os.Hostname()
		cfg.InstanceID = hostname
	}
	for name, value := range map[string]string{
		"TAMS_OPS_CAPACITY_URL":  cfg.TAMSURL,
		"TAMS_OPS_AUDIENCE":      cfg.TAMSAudience,
		"NOMAD_ADDR":             cfg.NomadAddress,
		"NOMAD_TOKEN":            cfg.NomadToken,
		"CONSUL_HTTP_ADDR":       cfg.ConsulAddr,
		"CONSUL_HTTP_TOKEN":      cfg.ConsulToken,
		"CONTROLLER_INSTANCE_ID": cfg.InstanceID,
	} {
		if value == "" {
			return config{}, fmt.Errorf("%s is required", name)
		}
	}
	if err := validateURL("TAMS_OPS_CAPACITY_URL", cfg.TAMSURL, true); err != nil {
		return config{}, err
	}
	if err := validateURL("TAMS_OPS_AUDIENCE", cfg.TAMSAudience, true); err != nil {
		return config{}, err
	}
	if err := validateSameOrigin(cfg.TAMSURL, cfg.TAMSAudience); err != nil {
		return config{}, err
	}
	if err := validateURL("NOMAD_ADDR", cfg.NomadAddress, false); err != nil {
		return config{}, err
	}
	if err := validateURL("CONSUL_HTTP_ADDR", cfg.ConsulAddr, false); err != nil {
		return config{}, err
	}
	intervalText := envOrDefault("RECONCILE_INTERVAL", "10s")
	interval, err := time.ParseDuration(intervalText)
	if err != nil || interval < 5*time.Second || interval > 30*time.Second {
		return config{}, fmt.Errorf("RECONCILE_INTERVAL must be a duration from 5s to 30s, got %q", intervalText)
	}
	cfg.Interval = interval

	return cfg, nil
}

func validateURL(name, value string, requireTLS bool) error {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" {
		return fmt.Errorf("%s must be an absolute HTTP URL", name)
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return fmt.Errorf("%s must use http or https", name)
	}
	if requireTLS && parsed.Scheme != "https" {
		return fmt.Errorf("%s must use https", name)
	}
	if parsed.User != nil {
		return fmt.Errorf("%s must not contain URL credentials", name)
	}
	if !requireTLS && parsed.Scheme == "http" && parsed.Hostname() != "127.0.0.1" && parsed.Hostname() != "localhost" && parsed.Hostname() != "::1" {
		return fmt.Errorf("%s may use cleartext HTTP only on loopback", name)
	}
	if name == "TAMS_OPS_CAPACITY_URL" && (parsed.Path != "/v1/ops/capacity" || parsed.RawQuery != "" || parsed.Fragment != "") {
		return fmt.Errorf("%s must be the exact /v1/ops/capacity endpoint without a query or fragment", name)
	}
	if name == "TAMS_OPS_AUDIENCE" && (parsed.RawQuery != "" || parsed.Fragment != "") {
		return fmt.Errorf("%s must not contain a query or fragment", name)
	}

	return nil
}

func validateSameOrigin(endpointValue, audienceValue string) error {
	endpoint, endpointErr := url.Parse(endpointValue)
	audience, audienceErr := url.Parse(audienceValue)
	if endpointErr != nil || audienceErr != nil {
		return errors.New("TAMS_OPS_CAPACITY_URL and TAMS_OPS_AUDIENCE must be valid URLs")
	}
	endpointPort := endpoint.Port()
	if endpointPort == "" {
		endpointPort = "443"
	}
	audiencePort := audience.Port()
	if audiencePort == "" {
		audiencePort = "443"
	}
	if !strings.EqualFold(endpoint.Scheme, audience.Scheme) || !strings.EqualFold(endpoint.Hostname(), audience.Hostname()) || endpointPort != audiencePort {
		return errors.New("TAMS_OPS_CAPACITY_URL and TAMS_OPS_AUDIENCE must use the same origin")
	}

	return nil
}

func envOrDefault(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}

	return fallback
}
