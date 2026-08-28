package provider

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/servicediscovery"
)

const (
	cachedPort        = 5008
	testNamespace     = "e2b"
	testLabelSelector = "app.kubernetes.io/name=orchestrator"
	testAPIEndpoint   = "https://dns-endpoint.example"
)

func TestFromConfig_SelectsTheAdapterCaseInsensitively(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		config servicediscovery.Config
		cached bool
	}{
		"DNS": {
			config: servicediscovery.Config{Provider: "DNS", OrchestratorPort: cachedPort, DNSQuery: []string{"orchestrator.service"}, DNSResolverAddress: "127.0.0.1:53"},
			cached: true,
		},
		"lowercase dns": {
			config: servicediscovery.Config{Provider: "dns", OrchestratorPort: cachedPort, DNSQuery: []string{"orchestrator.service"}, DNSResolverAddress: "127.0.0.1:53"},
			cached: true,
		},
		"NOMAD": {
			config: servicediscovery.Config{Provider: "NOMAD", OrchestratorPort: cachedPort, NomadEndpoint: "http://127.0.0.1:4646", NomadToken: "token"},
			cached: true,
		},
		"STATIC": {
			config: servicediscovery.Config{Provider: "STATIC", OrchestratorPort: cachedPort, StaticEndpoints: []string{"10.0.0.1"}},
			cached: false,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := New(t.Context(), tt.config, logger.NewNopLogger())
			require.NoError(t, err)

			// A cached backend has read nothing until its first refresh; a
			// direct one answers from configuration straight away. That
			// difference is what the caller sees, so assert it rather than the
			// concrete type, which the split puts out of reach.
			_, listErr := got.ListInstances(t.Context())
			if tt.cached {
				require.ErrorIs(t, listErr, servicediscovery.ErrNotYetSynced)

				return
			}
			require.NoError(t, listErr)
		})
	}
}

func TestFromConfig_RejectsIncompleteConfig(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		config  servicediscovery.Config
		wantErr error
	}{
		"unknown provider":           {config: servicediscovery.Config{Provider: "CONSUL"}},
		"DNS without resolver":       {config: servicediscovery.Config{Provider: "DNS", DNSQuery: []string{"orchestrator.service"}}, wantErr: ErrMissingDNSResolver},
		"DNS without query":          {config: servicediscovery.Config{Provider: "DNS", DNSResolverAddress: "127.0.0.1:53"}, wantErr: ErrMissingDNSQuery},
		"K8S-PODS without namespace": {config: servicediscovery.Config{Provider: "K8S-PODS", PodLabels: testLabelSelector}, wantErr: ErrMissingPodNamespace},
		"K8S-PODS without labels":    {config: servicediscovery.Config{Provider: "K8S-PODS", PodNamespace: testNamespace}, wantErr: ErrMissingPodLabels},
		"composed without the nomad side": {
			config:  servicediscovery.Config{Provider: NomadK8sPodsProvider, K8sAPIEndpoint: testAPIEndpoint, PodNamespace: testNamespace, PodLabels: testLabelSelector},
			wantErr: ErrMissingNomadEndpoint,
		},
		"composed without the kubernetes side": {
			config:  servicediscovery.Config{Provider: NomadK8sPodsProvider, NomadEndpoint: "http://127.0.0.1:4646", NomadToken: "token", K8sAPIEndpoint: testAPIEndpoint, PodLabels: testLabelSelector},
			wantErr: ErrMissingPodNamespace,
		},
		"NOMAD without endpoint":   {config: servicediscovery.Config{Provider: "NOMAD", NomadToken: "token"}, wantErr: ErrMissingNomadEndpoint},
		"NOMAD without token":      {config: servicediscovery.Config{Provider: "NOMAD", NomadEndpoint: "http://127.0.0.1:4646"}, wantErr: ErrMissingNomadToken},
		"STATIC without endpoints": {config: servicediscovery.Config{Provider: "STATIC"}, wantErr: ErrMissingStaticEndpoints},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := New(t.Context(), tt.config, logger.NewNopLogger())
			require.Error(t, err)
			assert.Nil(t, got)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
			}
		})
	}
}

func TestStatic_MapsEveryEndpointToTheConfiguredPort(t *testing.T) {
	t.Parallel()

	sd := servicediscovery.NewStatic([]string{"10.0.0.1", "10.0.0.2"}, cachedPort)

	items, err := sd.ListInstances(t.Context())
	require.NoError(t, err)
	require.Len(t, items, 2)

	assert.Equal(t, "10.0.0.1:5008", items[0].Address())
	assert.Equal(t, "10.0.0.2:5008", items[1].Address())
}

// Points GOOGLE_APPLICATION_CREDENTIALS at a service account so the Kubernetes
// side's credential resolves without leaving the test. Nothing here mints a
// token: the endpoint below refuses the connection first.
func applicationDefaultCredentials(t *testing.T) {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	der, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)

	credentials, err := json.Marshal(map[string]string{
		"type":           "service_account",
		"client_email":   "discovery@example.iam.gserviceaccount.com",
		"private_key_id": "test-key-id",
		"private_key":    string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})),
		"token_uri":      "https://oauth2.example/token",
	})
	require.NoError(t, err)

	path := filepath.Join(t.TempDir(), "adc.json")
	require.NoError(t, os.WriteFile(path, credentials, 0o600))
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", path)
}

// Both backends have to be in the composition and in this order: the union
// deduplicates with the primary winning, and a consumer enabling the mode
// expects its existing Nomad entries to survive. The Kubernetes side is pointed
// at a closed port, so a listing that reports "fallback" is the one that came
// back from Kubernetes.
//
//nolint:paralleltest // cannot call t.Setenv and t.Parallel
func TestFromConfig_ComposedModeUnionsNomadAsPrimaryAndKubernetesAsFallback(t *testing.T) {
	applicationDefaultCredentials(t)

	var polled atomic.Bool
	nomad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		polled.Store(true)
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode([]any{}); err != nil {
			t.Errorf("encoding nomad stub response: %v", err)
		}
	}))
	t.Cleanup(nomad.Close)

	sd, err := New(t.Context(), servicediscovery.Config{
		Provider:         strings.ToLower(NomadK8sPodsProvider),
		OrchestratorPort: cachedPort,
		NomadEndpoint:    nomad.URL,
		NomadToken:       "token",
		K8sAPIEndpoint:   "https://127.0.0.1:1",
		PodNamespace:     testNamespace,
		PodLabels:        testLabelSelector,
	}, logger.NewNopLogger())
	require.NoError(t, err)

	sd.Start(t.Context())
	t.Cleanup(func() { sd.Stop(t.Context()) })

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		_, listErr := sd.ListInstances(t.Context())
		assert.ErrorContains(c, listErr, "fallback discovery failed",
			"the Kubernetes side must be the fallback, and its failure must fail the listing")
	}, 5*time.Second, 10*time.Millisecond)

	assert.True(t, polled.Load(), "the Nomad side must be polled too")
}

// The provider key is what an operator types into config, so pin the literal:
// every other test names the constant, which drifts with it.
func TestFromConfig_ComposedKeyIsTheDocumentedLiteral(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "NOMAD+K8S-PODS", NomadK8sPodsProvider)
}
