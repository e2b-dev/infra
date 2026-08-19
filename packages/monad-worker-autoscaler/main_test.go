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

func setValidScaleOutEnvironment(t *testing.T) {
	t.Helper()
	setValidEnvironment(t)
	t.Setenv("MONAD_WORKER_AUTOSCALER_MODE", "scale-out")
	t.Setenv("MONAD_WORKER_AUTOSCALER_MUTATION_ENABLED", "scale-out-only")
	t.Setenv("MIG_PROJECT_ID", "monad-code")
	t.Setenv("MIG_REGION", "us-east4")
	t.Setenv("MIG_NAME", "e2b-orch-client-rig")
	t.Setenv("WORKER_HOST_FLOOR", "4")
}

func TestLoadConfigAcceptsScaleOutConfiguration(t *testing.T) { //nolint:paralleltest // t.Setenv cannot be used safely in a parallel test.
	setValidScaleOutEnvironment(t)
	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Mode != "scale-out" || cfg.MIGProject != "monad-code" || cfg.MIGRegion != "us-east4" || cfg.MIGName != "e2b-orch-client-rig" || cfg.Floor != 4 {
		t.Fatalf("unexpected config: %+v", cfg)
	}
}

func TestLoadConfigScaleOutRequiresDoubleKeyedMutation(t *testing.T) {
	for _, mutation := range []string{"", "false", "0", "true", "1", "scale-out", "enabled"} {
		setValidScaleOutEnvironment(t)
		t.Setenv("MONAD_WORKER_AUTOSCALER_MUTATION_ENABLED", mutation)
		if _, err := loadConfig(); err == nil || !strings.Contains(err.Error(), "scale-out-only") {
			t.Fatalf("expected double-keyed mutation guard for %q, got %v", mutation, err)
		}
	}
}

func TestLoadConfigScaleOutRequiresResizeTarget(t *testing.T) {
	for _, name := range []string{"MIG_PROJECT_ID", "MIG_REGION", "MIG_NAME", "WORKER_HOST_FLOOR"} {
		setValidScaleOutEnvironment(t)
		t.Setenv(name, "")
		if _, err := loadConfig(); err == nil {
			t.Fatalf("expected missing %s rejection", name)
		}
	}
	for _, floor := range []string{"1", "16", "0", "-2", "four", "4.5"} {
		setValidScaleOutEnvironment(t)
		t.Setenv("WORKER_HOST_FLOOR", floor)
		if _, err := loadConfig(); err == nil || !strings.Contains(err.Error(), "WORKER_HOST_FLOOR") {
			t.Fatalf("expected floor rejection for %q, got %v", floor, err)
		}
	}
}

func TestLoadConfigShadowRejectsResizeTargetLeftovers(t *testing.T) {
	for _, name := range []string{"MIG_PROJECT_ID", "MIG_REGION", "MIG_NAME", "WORKER_HOST_FLOOR"} {
		setValidEnvironment(t)
		t.Setenv(name, "leftover-value")
		if _, err := loadConfig(); err == nil || !strings.Contains(err.Error(), "shadow") {
			t.Fatalf("expected shadow half-configuration rejection for %s, got %v", name, err)
		}
	}
}

func TestLoadConfigRejectsUnknownMode(t *testing.T) {
	for _, mode := range []string{"", "mutate", "scale_out", "SHADOW"} {
		setValidEnvironment(t)
		t.Setenv("MONAD_WORKER_AUTOSCALER_MODE", mode)
		if _, err := loadConfig(); err == nil || !strings.Contains(err.Error(), "MONAD_WORKER_AUTOSCALER_MODE") {
			t.Fatalf("expected mode rejection for %q, got %v", mode, err)
		}
	}
}
