package envd

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/tests/integration/internal/api"
	"github.com/e2b-dev/infra/tests/integration/internal/envd"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"
	"github.com/e2b-dev/infra/tests/integration/internal/utils"
)

// generateSelfSignedCA produces a PEM-encoded self-signed CA certificate
// in-memory. Each call produces a unique certificate so tests can distinguish
// certA from certB without any shared state.
func generateSelfSignedCA(t *testing.T, cn string) string {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err, "generate key")

	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: cn},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err, "create certificate")

	var buf bytes.Buffer
	require.NoError(t, pem.Encode(&buf, &pem.Block{Type: "CERTIFICATE", Bytes: der}))

	return buf.String()
}

// postInitWithCA calls /init on the given sandbox with the provided PEM cert.
func postInitWithCA(t *testing.T, sbx *api.Sandbox, client *setup.EnvdClient, certPEM string) {
	t.Helper()

	now := time.Now()
	res, err := client.HTTPClient.PostInitWithResponse(
		t.Context(),
		envd.PostInitJSONRequestBody{
			CaBundle:  &certPEM,
			Timestamp: &now,
		},
		setup.WithSandbox(t, sbx.SandboxID),
	)

	require.NoError(t, err)
	require.Equal(t, http.StatusNoContent, res.StatusCode())
}

// readBundle execs cat on the CA bundle inside the sandbox and returns its content.
func readBundle(t *testing.T, sbx *api.Sandbox, client *setup.EnvdClient) string {
	t.Helper()

	out, err := utils.ExecCommandAsRootWithOutput(t, t.Context(), sbx, client,
		"cat", "/etc/ssl/certs/ca-certificates.crt")
	require.NoError(t, err, "read CA bundle")

	return out
}

// TestCACertBundleOnTmpfs verifies that /etc/ssl/certs is bind-mounted from
// tmpfs as set up by the envd.service ExecStartPre script. Without this mount
// atomic cert rotation would fail with EXDEV and writes would hit NBD.
func TestCACertBundleOnTmpfs(t *testing.T) {
	t.Parallel()

	c := setup.GetAPIClient()
	sbx := utils.SetupSandboxWithCleanup(t, c)
	client := setup.GetEnvdClient(t, t.Context())

	out, err := utils.ExecCommandAsRootWithOutput(t, t.Context(), sbx, client,
		"findmnt", "-T", "/etc/ssl/certs", "-o", "SOURCE,FSTYPE", "--noheadings")
	require.NoError(t, err)

	assert.Contains(t, out, "tmpfs", "/etc/ssl/certs should be on tmpfs")
}

// TestCACertInjection verifies that a CA cert sent via the caBundle field of
// /init is appended to the system trust bundle immediately.
func TestCACertInjection(t *testing.T) {
	t.Parallel()

	c := setup.GetAPIClient()
	sbx := utils.SetupSandboxWithCleanup(t, c)
	client := setup.GetEnvdClient(t, t.Context())

	certPEM := generateSelfSignedCA(t, "e2b-test-ca-inject")

	postInitWithCA(t, sbx, client, certPEM)

	bundle := readBundle(t, sbx, client)
	assert.Contains(t, bundle, strings.TrimRight(certPEM, "\n"),
		"injected cert should appear in CA bundle")
}

// TestCACertRotationOnResume simulates a sandbox pause/resume where the
// orchestrator supplies a different CA cert on resume (e.g. different egress
// proxy). The old cert must be removed and the new cert must be present.
func TestCACertRotationOnResume(t *testing.T) {
	t.Parallel()

	c := setup.GetAPIClient()
	sbx := utils.SetupSandboxWithCleanup(t, c)
	client := setup.GetEnvdClient(t, t.Context())

	certA := generateSelfSignedCA(t, "e2b-test-ca-A")
	certB := generateSelfSignedCA(t, "e2b-test-ca-B")

	normalizedA := strings.TrimRight(certA, "\n")
	normalizedB := strings.TrimRight(certB, "\n")

	// First /init — simulates sandbox creation.
	postInitWithCA(t, sbx, client, certA)

	bundle := readBundle(t, sbx, client)
	require.Contains(t, bundle, normalizedA, "certA should be in bundle after first /init")

	// Second /init with a different cert — simulates resume with new proxy cert.
	postInitWithCA(t, sbx, client, certB)

	// Poll until the background goroutine removes certA (up to 5 s).
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		bundle = readBundle(t, sbx, client)
		if !strings.Contains(bundle, normalizedA) {
			break
		}

		time.Sleep(100 * time.Millisecond)
	}

	assert.NotContains(t, bundle, normalizedA, "old cert should be removed after rotation")
	assert.Contains(t, bundle, normalizedB, "new cert should be present after rotation")
}

// TestCACertPersistsThroughUpdateCACertificates verifies that the injected cert
// survives a full bundle rebuild triggered by update-ca-certificates. This works
// because the background goroutine also writes the cert to
// /usr/local/share/ca-certificates/e2b-ca.crt, which is read as a source by
// update-ca-certificates.
func TestCACertPersistsThroughUpdateCACertificates(t *testing.T) {
	t.Parallel()

	c := setup.GetAPIClient()
	sbx := utils.SetupSandboxWithCleanup(t, c)
	client := setup.GetEnvdClient(t, t.Context())

	certPEM := generateSelfSignedCA(t, "e2b-test-ca-persist")

	postInitWithCA(t, sbx, client, certPEM)

	// Wait for the background goroutine to write to /usr/local/share/ca-certificates/.
	time.Sleep(500 * time.Millisecond)

	// Rebuild the bundle from source directories.
	err := utils.ExecCommandAsRoot(t, t.Context(), sbx, client, "update-ca-certificates")
	require.NoError(t, err, "update-ca-certificates should succeed")

	bundle := readBundle(t, sbx, client)
	assert.Contains(t, bundle, strings.TrimRight(certPEM, "\n"),
		"injected cert should survive update-ca-certificates rebuild")
}
