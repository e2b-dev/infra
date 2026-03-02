package proxy

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"math/big"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestGetTransportCredentials(t *testing.T) {
	t.Run("insecure by default", func(t *testing.T) {
		creds, err := getTransportCredentials(GrpcPausedSandboxResumerConfig{})
		require.NoError(t, err)
		require.Equal(t, "insecure", creds.Info().SecurityProtocol)
	})

	t.Run("tls enabled", func(t *testing.T) {
		creds, err := getTransportCredentials(GrpcPausedSandboxResumerConfig{
			TLSEnabled:    true,
			TLSServerName: "api.example.com",
		})
		require.NoError(t, err)
		require.Equal(t, "tls", creds.Info().SecurityProtocol)
	})

	t.Run("tls disabled with tls options fails", func(t *testing.T) {
		_, err := getTransportCredentials(GrpcPausedSandboxResumerConfig{
			TLSCABase64: base64.StdEncoding.EncodeToString([]byte("ca")),
		})
		require.Error(t, err)
	})

	t.Run("invalid tls ca fails", func(t *testing.T) {
		_, err := getTransportCredentials(GrpcPausedSandboxResumerConfig{
			TLSEnabled:  true,
			TLSCABase64: "invalid-base64",
		})
		require.Error(t, err)
	})

	t.Run("valid tls ca succeeds", func(t *testing.T) {
		caCertB64 := generateCACertificateBase64(t)
		creds, err := getTransportCredentials(GrpcPausedSandboxResumerConfig{
			TLSEnabled:  true,
			TLSCABase64: caCertB64,
		})
		require.NoError(t, err)
		require.Equal(t, "tls", creds.Info().SecurityProtocol)
	})

	t.Run("missing mtls key fails", func(t *testing.T) {
		_, err := getTransportCredentials(GrpcPausedSandboxResumerConfig{
			TLSEnabled:       true,
			TLSClientCertB64: base64.StdEncoding.EncodeToString([]byte("cert")),
		})
		require.Error(t, err)
	})

	t.Run("invalid mtls pair fails", func(t *testing.T) {
		_, err := getTransportCredentials(GrpcPausedSandboxResumerConfig{
			TLSEnabled:       true,
			TLSClientCertB64: base64.StdEncoding.EncodeToString([]byte("cert")),
			TLSClientKeyB64:  base64.StdEncoding.EncodeToString([]byte("key")),
		})
		require.Error(t, err)
	})

	t.Run("valid mtls pair succeeds", func(t *testing.T) {
		certB64, keyB64 := generateClientCertificatePairBase64(t)
		creds, err := getTransportCredentials(GrpcPausedSandboxResumerConfig{
			TLSEnabled:       true,
			TLSClientCertB64: certB64,
			TLSClientKeyB64:  keyB64,
		})
		require.NoError(t, err)
		require.Equal(t, "tls", creds.Info().SecurityProtocol)
	})
}

func generateCACertificateBase64(t *testing.T) string {
	t.Helper()

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	require.NoError(t, err)

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})

	return base64.StdEncoding.EncodeToString(certPEM)
}

func generateClientCertificatePairBase64(t *testing.T) (string, string) {
	t.Helper()

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	template := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "client"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	require.NoError(t, err)

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(privateKey)})

	return base64.StdEncoding.EncodeToString(certPEM), base64.StdEncoding.EncodeToString(keyPEM)
}
