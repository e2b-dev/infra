package kubernetesserver

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testConfig(envdPort int32) Config {
	return Config{
		Namespace:             "e2b-test",
		AllowedRuntimeClasses: []string{RuntimeClassCLH, RuntimeClassQEMU},
		DefaultRuntimeClass:   RuntimeClassCLH,
		SandboxImageTemplate:  "registry.example/e2b/{template_id}:{build_id}",
		EnvdCommand:           "/usr/bin/envd",
		EnvdArgs:              []string{"-isnotfc", "-no-cgroups"},
		EnvdPort:              envdPort,
		ServiceAccountName:    "e2b-sandbox",
		ImagePullPolicy:       "IfNotPresent",
		DNSNameservers:        []string{"8.8.8.8"},
		CreateTimeout:         5 * time.Second,
		DeleteTimeout:         5 * time.Second,
		GRPCPort:              5008,
		ProxyPort:             5007,
		HealthPort:            5018,
		NodeID:                "kubernetes-orchestrator-test",
		ServiceVersion:        "test",
		ServiceCommit:         "test",
		CapacityCPU:           16,
		CapacityMemoryMiB:     32768,
		CPUArchitecture:       "x86_64",
	}
}

func TestConfigSelectsAllowedKataRuntime(t *testing.T) {
	cfg := testConfig(defaultEnvdPort)

	selected, err := cfg.runtimeClass(nil)
	require.NoError(t, err)
	assert.Equal(t, RuntimeClassCLH, selected)

	selected, err = cfg.runtimeClass(map[string]string{RuntimeClassMetadataKey: RuntimeClassQEMU})
	require.NoError(t, err)
	assert.Equal(t, RuntimeClassQEMU, selected)

	_, err = cfg.runtimeClass(map[string]string{RuntimeClassMetadataKey: "kata-fc"})
	require.ErrorContains(t, err, "not allowed")
}

func TestConfigResolvesTemplateImage(t *testing.T) {
	cfg := testConfig(defaultEnvdPort)
	image, err := cfg.sandboxImage("base-python", "build_42")
	require.NoError(t, err)
	assert.Equal(t, "registry.example/e2b/base-python:build_42", image)

	_, err = cfg.sandboxImage("../escape", "build")
	require.ErrorContains(t, err, "image-safe")
}

func TestConfigRejectsUnsupportedRuntimeClass(t *testing.T) {
	cfg := testConfig(defaultEnvdPort)
	cfg.AllowedRuntimeClasses = []string{"kata-fc"}
	cfg.DefaultRuntimeClass = "kata-fc"
	require.ErrorContains(t, cfg.Validate(), "unsupported RuntimeClass")
}

func TestConfigRejectsIncompleteOrInvalidImageTemplate(t *testing.T) {
	cfg := testConfig(defaultEnvdPort)
	cfg.SandboxImageTemplate = "registry.example/e2b/sandbox:latest"
	require.ErrorContains(t, cfg.Validate(), "must contain")

	cfg = testConfig(defaultEnvdPort)
	_, err := cfg.sandboxImage("UPPERCASE", "build-42")
	require.ErrorContains(t, err, "invalid")
}

func TestNormalizedArchitectureMatchesBuildMetadata(t *testing.T) {
	assert.Equal(t, "x86_64", normalizedArchitecture("amd64"))
	assert.Equal(t, "aarch64", normalizedArchitecture("arm64"))
}

func TestConfigRejectsClusterInternalDNSNameserver(t *testing.T) {
	cfg := testConfig(defaultEnvdPort)
	cfg.DNSNameservers = []string{"172.30.0.10"}
	require.ErrorContains(t, cfg.Validate(), "must be a public IP address")
}
