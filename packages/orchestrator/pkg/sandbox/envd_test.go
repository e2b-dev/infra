package sandbox

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coreos/go-iptables/iptables"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/envd"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/network"
)

// mockEgressProxy is a test EgressProxy that returns a fixed CA bundle string.
type mockEgressProxy struct {
	bundle string
}

func (m *mockEgressProxy) OnSlotCreate(_ *network.Slot, _ *iptables.IPTables) error { return nil }
func (m *mockEgressProxy) OnSlotDelete(_ *network.Slot, _ *iptables.IPTables) error { return nil }
func (m *mockEgressProxy) CABundle() string                                         { return m.bundle }

// newTestSandboxWithBundle builds a minimal Sandbox with CABundle set —
// mirroring what Factory.CreateSandbox does with f.egressProxy.CABundle().
func newTestSandboxWithBundle(bundle string) *Sandbox {
	return &Sandbox{
		Metadata: &Metadata{
			internalConfig: internalConfig{EnvdInitRequestTimeout: 5 * time.Second},
			Config:         NewConfig(Config{}),
			Runtime:        RuntimeMetadata{SandboxID: "test-sandbox"},
		},
		CABundle: bundle,
	}
}

// TestEnvdInitSendsCaBundle verifies the full injection chain:
// EgressProxy.CABundle() → Sandbox.CABundle → POST /init body caBundle field.
//
// Not parallel: overrides the package-level sandboxHttpClient.
func TestEnvdInitSendsCaBundle(t *testing.T) { //nolint:paralleltest
	const pemBundle = "-----BEGIN CERTIFICATE-----\nPROXY\n-----END CERTIFICATE-----\n" +
		"-----BEGIN CERTIFICATE-----\nCUSTOM\n-----END CERTIFICATE-----\n"

	proxy := &mockEgressProxy{bundle: pemBundle}
	sbx := newTestSandboxWithBundle(proxy.CABundle())

	var captured envd.PostInitJSONBody
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/init", r.URL.Path)

		err := json.NewDecoder(r.Body).Decode(&captured)
		assert.NoError(t, err)

		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	orig := sandboxHttpClient
	sandboxHttpClient = http.Client{Timeout: 5 * time.Second}
	defer func() { sandboxHttpClient = orig }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, _, err := sbx.doRequestWithInfiniteRetries(ctx, http.MethodPost, server.URL+"/init")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.NotEmpty(t, captured.CaBundle, "caBundle should be non-empty")
	assert.Contains(t, captured.CaBundle, "-----BEGIN CERTIFICATE-----\nPROXY\n-----END CERTIFICATE-----")
	assert.Contains(t, captured.CaBundle, "-----BEGIN CERTIFICATE-----\nCUSTOM\n-----END CERTIFICATE-----")
}

func TestEnvdInitEmptyCaBundle(t *testing.T) { //nolint:paralleltest
	sbx := newTestSandboxWithBundle("")

	var captured envd.PostInitJSONBody
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&captured)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	orig := sandboxHttpClient
	sandboxHttpClient = http.Client{Timeout: 5 * time.Second}
	defer func() { sandboxHttpClient = orig }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, _, err := sbx.doRequestWithInfiniteRetries(ctx, http.MethodPost, server.URL+"/init")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Empty(t, captured.CaBundle, "caBundle should be omitted when empty")
}
