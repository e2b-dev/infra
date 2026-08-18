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
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/tests/integration/internal/api"
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

// TestCACertPersistsThroughUpdateCACertificates checks the platform assumption
// envd's CA installer is built on: that a cert dropped in its extra-certs path is
// picked up when update-ca-certificates rebuilds the trust bundle from its source
// directories. envd persists every injected cert there for exactly that reason,
// so if the template's tooling or layout ever stopped honouring the directory,
// injected certs would silently vanish on the next rebuild.
//
// It deliberately exercises the guest, not envd: the cert is written straight to
// the path rather than through /init, which is how the orchestrator delivers it
// in production but is not reachable through the sandbox URL. envd's own side —
// that it appends, rotates and persists the cert — is covered by
// TestInstallCACert_* in packages/envd/internal/host.
func TestCACertPersistsThroughUpdateCACertificates(t *testing.T) {
	t.Parallel()

	c := setup.GetAPIClient()
	sbx := utils.SetupSandboxWithCleanup(t, c)
	client := setup.GetEnvdClient(t, t.Context())

	certPEM := generateSelfSignedCA(t, "e2b-test-ca-persist")

	// caExtraPath in packages/envd/internal/host/cacerts.go, which this module
	// cannot import (it is internal to envd). Change it there, change it here —
	// otherwise this keeps passing while testing a path envd no longer writes.
	const caExtraPath = "/usr/local/share/ca-certificates/e2b-ca.crt"

	utils.UploadFileAs(t, t.Context(), sbx, client, caExtraPath, certPEM, "root")

	// Rebuild the bundle from source directories.
	err := utils.ExecCommandAsRoot(t, t.Context(), sbx, client, "update-ca-certificates")
	require.NoError(t, err, "update-ca-certificates should succeed")

	bundle := readBundle(t, sbx, client)
	assert.Contains(t, bundle, strings.TrimRight(certPEM, "\n"),
		"a cert in envd's extra-certs path should survive an update-ca-certificates rebuild")
}
