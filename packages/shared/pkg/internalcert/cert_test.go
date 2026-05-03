package internalcert

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestEnsureNoCAConfigured(t *testing.T) {
	result, err := Ensure(context.Background(), Config{})
	require.NoError(t, err)
	require.False(t, result.Issued)
	require.True(t, result.Expiry.IsZero())
}

func TestEnsureReusesExistingCertificate(t *testing.T) {
	dir := t.TempDir()
	certFile := filepath.Join(dir, "cert.pem")
	keyFile := filepath.Join(dir, "key.pem")
	expiresAt := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)

	writeTestCertificate(t, certFile, expiresAt)
	require.NoError(t, os.WriteFile(keyFile, []byte("test-key"), 0o600))

	result, err := Ensure(context.Background(), Config{
		CertFile:    certFile,
		KeyFile:     keyFile,
		CAPool:      "projects/test/locations/us-central1/caPools/test",
		DNSName:     "api.internal.example.com",
		RenewBefore: 24 * time.Hour,
		Now: func() time.Time {
			return time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
		},
	})

	require.NoError(t, err)
	require.False(t, result.Issued)
	require.Equal(t, expiresAt, result.Expiry)
}

func writeTestCertificate(t *testing.T, path string, expiresAt time.Time) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	certDER, err := x509.CreateCertificate(rand.Reader, &x509.Certificate{
		SerialNumber: big.NewInt(1),
		NotBefore:    time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		NotAfter:     expiresAt,
		DNSNames:     []string{"api.internal.example.com"},
	}, &x509.Certificate{
		SerialNumber: big.NewInt(1),
		NotBefore:    time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		NotAfter:     expiresAt,
	}, &key.PublicKey, key)
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER}), 0o644))
}
