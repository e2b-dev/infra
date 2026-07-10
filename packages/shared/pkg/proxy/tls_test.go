package proxy

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/proxy/pool"
)

type testCA struct {
	cert    *x509.Certificate
	key     *ecdsa.PrivateKey
	certPEM []byte
	pool    *x509.CertPool
}

type testCert struct {
	certPEM []byte
	keyPEM  []byte
	certFile string
	keyFile  string
}

func newTestCA(t *testing.T) *testCA {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{Organization: []string{"Test"}, CommonName: "Test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	require.NoError(t, err)

	cert, err := x509.ParseCertificate(certDER)
	require.NoError(t, err)

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})

	certPool := x509.NewCertPool()
	certPool.AddCert(cert)

	return &testCA{cert: cert, key: key, certPEM: certPEM, pool: certPool}
}

func (ca *testCA) issueCert(t *testing.T, hosts ...string) *testCert {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	template := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "test-server"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}

	for _, h := range hosts {
		if ip := net.ParseIP(h); ip != nil {
			template.IPAddresses = append(template.IPAddresses, ip)
		} else {
			template.DNSNames = append(template.DNSNames, h)
		}
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, ca.cert, &key.PublicKey, ca.key)
	require.NoError(t, err)

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})

	keyDER, err := x509.MarshalECPrivateKey(key)
	require.NoError(t, err)
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	dir := t.TempDir()
	certFile := filepath.Join(dir, "cert.pem")
	keyFile := filepath.Join(dir, "key.pem")
	require.NoError(t, os.WriteFile(certFile, certPEM, 0600))
	require.NoError(t, os.WriteFile(keyFile, keyPEM, 0600))

	return &testCert{certPEM: certPEM, keyPEM: keyPEM, certFile: certFile, keyFile: keyFile}
}

func TestListenAndServeTLS(t *testing.T) {
	t.Parallel()

	ca := newTestCA(t)
	cert := ca.issueCert(t, "127.0.0.1", "localhost")

	backend := startPlaintextBackend(t)

	proxy := New(
		0,
		ClientProxyRetries,
		20*time.Second,
		func(*http.Request) (*pool.Destination, error) {
			return &pool.Destination{
				Url:           backend,
				SandboxId:     "test",
				RequestLogger: logger.NewNopLogger(),
				ConnectionKey: "test",
			}, nil
		},
		nil,
		false,
	)

	var lisCfg net.ListenConfig
	l, err := lisCfg.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()

	proxy.Addr = fmt.Sprintf("127.0.0.1:%d", port)

	go func() {
		_ = proxy.ListenAndServeTLS(t.Context(), cert.certFile, cert.keyFile)
	}()
	t.Cleanup(func() { proxy.Close() })

	waitForPort(t, port)

	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: ca.pool},
		},
	}

	resp, err := client.Get(fmt.Sprintf("https://127.0.0.1:%d/hello", port))
	require.NoError(t, err)
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "ok", string(body))
}

func TestListenAndServeTLS_WrongCA_Rejected(t *testing.T) {
	t.Parallel()

	ca := newTestCA(t)
	wrongCA := newTestCA(t)
	cert := ca.issueCert(t, "127.0.0.1")

	backend := startPlaintextBackend(t)

	proxy := New(
		0,
		ClientProxyRetries,
		20*time.Second,
		func(*http.Request) (*pool.Destination, error) {
			return &pool.Destination{
				Url:           backend,
				SandboxId:     "test",
				RequestLogger: logger.NewNopLogger(),
				ConnectionKey: "test",
			}, nil
		},
		nil,
		false,
	)

	var lisCfg net.ListenConfig
	l, err := lisCfg.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()

	proxy.Addr = fmt.Sprintf("127.0.0.1:%d", port)

	go func() {
		_ = proxy.ListenAndServeTLS(t.Context(), cert.certFile, cert.keyFile)
	}()
	t.Cleanup(func() { proxy.Close() })

	waitForPort(t, port)

	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: wrongCA.pool},
		},
	}

	_, err = client.Get(fmt.Sprintf("https://127.0.0.1:%d/hello", port))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "certificate signed by unknown authority")
}

func TestWithUpstreamTLS(t *testing.T) {
	t.Parallel()

	ca := newTestCA(t)
	cert := ca.issueCert(t, "127.0.0.1")

	backend := startTLSBackend(t, cert)

	proxy := New(
		0,
		ClientProxyRetries,
		20*time.Second,
		func(*http.Request) (*pool.Destination, error) {
			return &pool.Destination{
				Url:           backend,
				SandboxId:     "test",
				RequestLogger: logger.NewNopLogger(),
				ConnectionKey: "test",
			}, nil
		},
		nil,
		false,
		WithUpstreamTLS(&tls.Config{RootCAs: ca.pool, MinVersion: tls.VersionTLS12}),
	)

	var lisCfg net.ListenConfig
	l, err := lisCfg.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := l.Addr().(*net.TCPAddr).Port

	go func() {
		_ = proxy.Serve(l)
	}()
	t.Cleanup(func() { proxy.Close() })

	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/hello", port))
	require.NoError(t, err)
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "ok", string(body))
}

func TestWithUpstreamTLS_WrongCA_Fails(t *testing.T) {
	t.Parallel()

	ca := newTestCA(t)
	wrongCA := newTestCA(t)
	cert := ca.issueCert(t, "127.0.0.1")

	backend := startTLSBackend(t, cert)

	proxy := New(
		0,
		ClientProxyRetries,
		20*time.Second,
		func(*http.Request) (*pool.Destination, error) {
			return &pool.Destination{
				Url:           backend,
				SandboxId:     "test",
				RequestLogger: logger.NewNopLogger(),
				ConnectionKey: "test",
			}, nil
		},
		nil,
		false,
		WithUpstreamTLS(&tls.Config{RootCAs: wrongCA.pool, MinVersion: tls.VersionTLS12}),
	)

	var lisCfg net.ListenConfig
	l, err := lisCfg.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := l.Addr().(*net.TCPAddr).Port

	go func() {
		_ = proxy.Serve(l)
	}()
	t.Cleanup(func() { proxy.Close() })

	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/hello", port))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusBadGateway, resp.StatusCode)
}

func TestPlaintextFallbackWhenNoTLSConfigured(t *testing.T) {
	t.Parallel()

	backend := startPlaintextBackend(t)

	proxy := New(
		0,
		ClientProxyRetries,
		20*time.Second,
		func(*http.Request) (*pool.Destination, error) {
			return &pool.Destination{
				Url:           backend,
				SandboxId:     "test",
				RequestLogger: logger.NewNopLogger(),
				ConnectionKey: "test",
			}, nil
		},
		nil,
		false,
	)

	var lisCfg net.ListenConfig
	l, err := lisCfg.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := l.Addr().(*net.TCPAddr).Port

	go func() {
		_ = proxy.Serve(l)
	}()
	t.Cleanup(func() { proxy.Close() })

	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/hello", port))
	require.NoError(t, err)
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "ok", string(body))
}

func startPlaintextBackend(t *testing.T) *url.URL {
	t.Helper()

	var lisCfg net.ListenConfig
	l, err := lisCfg.Listen(context.Background(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)

	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("ok"))
		}),
	}
	go srv.Serve(l)
	t.Cleanup(func() { srv.Close() })

	u, _ := url.Parse(fmt.Sprintf("http://%s", l.Addr().String()))
	return u
}

func startTLSBackend(t *testing.T, cert *testCert) *url.URL {
	t.Helper()

	tlsCert, err := tls.X509KeyPair(cert.certPEM, cert.keyPEM)
	require.NoError(t, err)

	var lisCfg net.ListenConfig
	l, err := lisCfg.Listen(context.Background(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)

	tlsListener := tls.NewListener(l, &tls.Config{
		Certificates: []tls.Certificate{tlsCert},
		MinVersion:   tls.VersionTLS12,
	})

	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("ok"))
		}),
	}
	go srv.Serve(tlsListener)
	t.Cleanup(func() { srv.Close() })

	u, _ := url.Parse(fmt.Sprintf("https://%s", l.Addr().String()))
	return u
}

func waitForPort(t *testing.T, port int) {
	t.Helper()

	addr := fmt.Sprintf("127.0.0.1:%d", port)
	for range 50 {
		conn, err := net.DialTimeout("tcp", addr, 50*time.Millisecond)
		if err == nil {
			conn.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("port %d did not become available", port)
}
