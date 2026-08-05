package main

import (
	"strings"
	"testing"
)

func TestLoadConfigIsShadowOnly(t *testing.T) {
	setValidEnvironment(t)
	t.Setenv("MONAD_WORKER_AUTOSCALER_MUTATION_ENABLED", "true")
	if _, err := loadConfig(); err == nil || !strings.Contains(err.Error(), "mutation") {
		t.Fatalf("expected mutation guard, got %v", err)
	}
}

func TestLoadConfigRejectsCredentialExposure(t *testing.T) {
	setValidEnvironment(t)
	t.Setenv("NOMAD_ADDR", "http://nomad.example.com:4646")
	if _, err := loadConfig(); err == nil || !strings.Contains(err.Error(), "loopback") {
		t.Fatalf("expected cleartext URL guard, got %v", err)
	}

	setValidEnvironment(t)
	t.Setenv("TAMS_OPS_CAPACITY_URL", "https://token@example.com/v1/ops/capacity")
	if _, err := loadConfig(); err == nil || !strings.Contains(err.Error(), "credentials") {
		t.Fatalf("expected URL credential guard, got %v", err)
	}
}

func TestLoadConfigRejectsCrossOriginIdentityDelivery(t *testing.T) {
	setValidEnvironment(t)
	t.Setenv("TAMS_OPS_CAPACITY_URL", "https://attacker.example/v1/ops/capacity")
	if _, err := loadConfig(); err == nil || !strings.Contains(err.Error(), "same origin") {
		t.Fatalf("expected endpoint/audience origin guard, got %v", err)
	}
}

func TestLoadConfigAcceptsBoundedShadowConfiguration(t *testing.T) { //nolint:paralleltest // t.Setenv cannot be used safely in a parallel test.
	setValidEnvironment(t)
	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.NomadPool != "default" || cfg.Interval.String() != "10s" {
		t.Fatalf("unexpected config: %+v", cfg)
	}
}

func setValidEnvironment(t *testing.T) {
	t.Helper()
	t.Setenv("MONAD_WORKER_AUTOSCALER_MODE", "shadow")
	t.Setenv("MONAD_WORKER_AUTOSCALER_MUTATION_ENABLED", "false")
	t.Setenv("TAMS_OPS_CAPACITY_URL", "https://api.tams.monad0.net/v1/ops/capacity")
	t.Setenv("TAMS_OPS_AUDIENCE", "https://api.tams.monad0.net")
	t.Setenv("NOMAD_ADDR", "http://127.0.0.1:4646")
	t.Setenv("NOMAD_TOKEN", "nomad")
	t.Setenv("NOMAD_NODE_POOL", "default")
	t.Setenv("CONSUL_HTTP_ADDR", "http://127.0.0.1:8500")
	t.Setenv("CONSUL_HTTP_TOKEN", "consul")
	t.Setenv("CONTROLLER_INSTANCE_ID", "test-allocation")
	t.Setenv("RECONCILE_INTERVAL", "10s")
}
