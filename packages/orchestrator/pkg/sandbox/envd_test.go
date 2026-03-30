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

// mockEgressProxy is a test EgressProxy that returns a fixed set of CA certificates.
type mockEgressProxy struct {
	certs []network.CACertificate
}

func (m *mockEgressProxy) OnSlotCreate(_ *network.Slot, _ *iptables.IPTables) error { return nil }
func (m *mockEgressProxy) OnSlotDelete(_ *network.Slot, _ *iptables.IPTables) error { return nil }
func (m *mockEgressProxy) CACertificates() []network.CACertificate                  { return m.certs }

// newTestSandboxWithCerts builds a minimal Sandbox that has CACertificates set —
// mirroring what Factory.CreateSandbox does with f.egressProxy.CACertificates().
func newTestSandboxWithCerts(certs []network.CACertificate) *Sandbox {
	return &Sandbox{
		Metadata: &Metadata{
			internalConfig: internalConfig{EnvdInitRequestTimeout: 5 * time.Second},
			Config:         NewConfig(Config{}),
			Runtime:        RuntimeMetadata{SandboxID: "test-sandbox"},
		},
		CACertificates: certs,
	}
}

func TestConvertCACertificates(t *testing.T) {
	t.Parallel()

	t.Run("converts network certs to envd certs preserving name and PEM", func(t *testing.T) {
		t.Parallel()

		proxy := &mockEgressProxy{
			certs: []network.CACertificate{
				{Name: "proxy-ca", Cert: "-----BEGIN CERTIFICATE-----\nABC\n-----END CERTIFICATE-----\n"},
				{Name: "custom-ca", Cert: "-----BEGIN CERTIFICATE-----\nDEF\n-----END CERTIFICATE-----\n"},
			},
		}

		sbx := newTestSandboxWithCerts(proxy.CACertificates())
		result := sbx.convertCACertificates(sbx.CACertificates)

		require.Len(t, result, 2)
		assert.Equal(t, "proxy-ca", result[0].Name)
		assert.Equal(t, "-----BEGIN CERTIFICATE-----\nABC\n-----END CERTIFICATE-----\n", result[0].Cert)
		assert.Equal(t, "custom-ca", result[1].Name)
		assert.Equal(t, "-----BEGIN CERTIFICATE-----\nDEF\n-----END CERTIFICATE-----\n", result[1].Cert)
	})

	t.Run("returns empty slice for nil certs", func(t *testing.T) {
		t.Parallel()
		sbx := newTestSandboxWithCerts(nil)
		result := sbx.convertCACertificates(nil)
		assert.Empty(t, result)
	})
}

// TestEnvdInitSendsCACertificates verifies the full injection chain:
// EgressProxy.CACertificates() → Sandbox.CACertificates → POST /init body.
func TestEnvdInitSendsCACertificates(t *testing.T) {
	// Not parallel: overrides the package-level sandboxHttpClient.

	proxy := &mockEgressProxy{
		certs: []network.CACertificate{
			{Name: "proxy-ca", Cert: "-----BEGIN CERTIFICATE-----\nPROXY\n-----END CERTIFICATE-----\n"},
			{Name: "custom-ca", Cert: "-----BEGIN CERTIFICATE-----\nCUSTOM\n-----END CERTIFICATE-----\n"},
		},
	}

	// Simulate what Factory.CreateSandbox does when assigning egress proxy certs.
	sbx := newTestSandboxWithCerts(proxy.CACertificates())

	var captured envd.PostInitJSONBody
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/init", r.URL.Path)

		err := json.NewDecoder(r.Body).Decode(&captured)
		require.NoError(t, err)

		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	// Temporarily swap the package-level client so the sandbox reaches our test server.
	orig := sandboxHttpClient
	sandboxHttpClient = http.Client{Timeout: 5 * time.Second}
	defer func() { sandboxHttpClient = orig }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, _, err := sbx.doRequestWithInfiniteRetries(ctx, http.MethodPost, server.URL+"/init")
	require.NoError(t, err)

	require.Len(t, captured.CaCertificates, 2)
	assert.Equal(t, proxy.certs[0].Name, captured.CaCertificates[0].Name)
	assert.Equal(t, proxy.certs[0].Cert, captured.CaCertificates[0].Cert)
	assert.Equal(t, proxy.certs[1].Name, captured.CaCertificates[1].Name)
	assert.Equal(t, proxy.certs[1].Cert, captured.CaCertificates[1].Cert)
}
