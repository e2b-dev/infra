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
	privateca "google.golang.org/api/privateca/v1"
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

func TestEnsureRenewsExpiringCertificate(t *testing.T) {
	dir := t.TempDir()
	certFile := filepath.Join(dir, "cert.pem")
	keyFile := filepath.Join(dir, "key.pem")
	existingExpiresAt := time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC)
	renewedExpiresAt := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)

	writeTestCertificate(t, certFile, existingExpiresAt)
	require.NoError(t, os.WriteFile(keyFile, []byte("test-key"), 0o600))

	issueCalls := 0
	result, err := Ensure(context.Background(), Config{
		CertFile:    certFile,
		KeyFile:     keyFile,
		CAPool:      "projects/test/locations/us-central1/caPools/test",
		DNSName:     "api.internal.example.com",
		RenewBefore: 30 * 24 * time.Hour,
		Now: func() time.Time {
			return time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
		},
		Issuer: func(ctx context.Context, config Config, csr string, lifetime time.Duration) (*privateca.Certificate, error) {
			issueCalls++
			return testPrivateCACertificate(t, "api.internal.example.com", renewedExpiresAt), nil
		},
	})

	require.NoError(t, err)
	require.True(t, result.Issued)
	require.Equal(t, 1, issueCalls)
	require.Equal(t, renewedExpiresAt, result.Expiry)
}

func TestEnsureWithRetry(t *testing.T) {
	dir := t.TempDir()
	certFile := filepath.Join(dir, "cert.pem")
	keyFile := filepath.Join(dir, "key.pem")

	attempts := 0
	result, err := EnsureWithRetry(context.Background(), Config{
		CertFile: certFile,
		KeyFile:  keyFile,
		CAPool:   "projects/test/locations/us-central1/caPools/test",
		DNSName:  "api.internal.example.com",
		Issuer: func(ctx context.Context, config Config, csr string, lifetime time.Duration) (*privateca.Certificate, error) {
			attempts++
			if attempts == 1 {
				return nil, context.DeadlineExceeded
			}

			return testPrivateCACertificate(t, "api.internal.example.com", time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)), nil
		},
	}, RetryConfig{
		Attempts: 2,
		Delay:    time.Millisecond,
	})

	require.NoError(t, err)
	require.True(t, result.Issued)
	require.Equal(t, 2, attempts)
}

func TestCertificateIDStartsWithLetter(t *testing.T) {
	id := certificateID("123_bad_prefix")

	require.Regexp(t, `^[a-z][-a-z0-9]*$`, id)
	require.LessOrEqual(t, len(id), 63)
}

func testPrivateCACertificate(t *testing.T, dnsName string, expiresAt time.Time) *privateca.Certificate {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	certDER, err := x509.CreateCertificate(rand.Reader, &x509.Certificate{
		SerialNumber: big.NewInt(2),
		NotBefore:    time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		NotAfter:     expiresAt,
		DNSNames:     []string{dnsName},
	}, &x509.Certificate{
		SerialNumber: big.NewInt(2),
		NotBefore:    time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		NotAfter:     expiresAt,
	}, &key.PublicKey, key)
	require.NoError(t, err)

	return &privateca.Certificate{
		PemCertificate: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})),
	}
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
