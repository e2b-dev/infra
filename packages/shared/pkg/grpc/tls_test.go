package grpc

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"

	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

type testCA struct {
	cert    *x509.Certificate
	key     *ecdsa.PrivateKey
	certPEM []byte
	pool    *x509.CertPool
}

type testCert struct {
	certPEM  []byte
	keyPEM   []byte
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

func TestWithTLS_ServerAcceptsTLSConnections(t *testing.T) {
	t.Parallel()

	ca := newTestCA(t)
	cert := ca.issueCert(t, "127.0.0.1")

	tel := &telemetry.Client{}
	srv := NewGRPCServer(tel, WithTLS(cert.certFile, cert.keyFile))
	healthpb.RegisterHealthServer(srv, &healthpb.UnimplementedHealthServer{})

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go srv.Serve(lis)
	t.Cleanup(func() { srv.Stop() })

	creds := credentials.NewTLS(&tls.Config{RootCAs: ca.pool})
	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(creds))
	require.NoError(t, err)
	defer conn.Close()

	client := healthpb.NewHealthClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err = client.Check(ctx, &healthpb.HealthCheckRequest{})
	// Unimplemented means the TLS handshake and gRPC framing succeeded
	assertGRPCReachable(t, err)
}

func TestWithTLSFromPEM_ServerAcceptsTLSConnections(t *testing.T) {
	t.Parallel()

	ca := newTestCA(t)
	cert := ca.issueCert(t, "127.0.0.1")

	tel := &telemetry.Client{}
	srv := NewGRPCServer(tel, WithTLSFromPEM(cert.certPEM, cert.keyPEM))
	healthpb.RegisterHealthServer(srv, &healthpb.UnimplementedHealthServer{})

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go srv.Serve(lis)
	t.Cleanup(func() { srv.Stop() })

	creds := credentials.NewTLS(&tls.Config{RootCAs: ca.pool})
	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(creds))
	require.NoError(t, err)
	defer conn.Close()

	client := healthpb.NewHealthClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err = client.Check(ctx, &healthpb.HealthCheckRequest{})
	assertGRPCReachable(t, err)
}

func TestWithTLS_WrongCA_Rejected(t *testing.T) {
	t.Parallel()

	ca := newTestCA(t)
	wrongCA := newTestCA(t)
	cert := ca.issueCert(t, "127.0.0.1")

	tel := &telemetry.Client{}
	srv := NewGRPCServer(tel, WithTLS(cert.certFile, cert.keyFile))
	healthpb.RegisterHealthServer(srv, &healthpb.UnimplementedHealthServer{})

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go srv.Serve(lis)
	t.Cleanup(func() { srv.Stop() })

	creds := credentials.NewTLS(&tls.Config{RootCAs: wrongCA.pool})
	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(creds))
	require.NoError(t, err)
	defer conn.Close()

	client := healthpb.NewHealthClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err = client.Check(ctx, &healthpb.HealthCheckRequest{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "certificate signed by unknown authority")
}

func TestNoTLS_PlaintextFallback(t *testing.T) {
	t.Parallel()

	tel := &telemetry.Client{}
	srv := NewGRPCServer(tel)
	healthpb.RegisterHealthServer(srv, &healthpb.UnimplementedHealthServer{})

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go srv.Serve(lis)
	t.Cleanup(func() { srv.Stop() })

	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	defer conn.Close()

	client := healthpb.NewHealthClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err = client.Check(ctx, &healthpb.HealthCheckRequest{})
	assertGRPCReachable(t, err)
}

func TestWithTLS_InsecureClient_Fails(t *testing.T) {
	t.Parallel()

	ca := newTestCA(t)
	cert := ca.issueCert(t, "127.0.0.1")

	tel := &telemetry.Client{}
	srv := NewGRPCServer(tel, WithTLS(cert.certFile, cert.keyFile))
	healthpb.RegisterHealthServer(srv, &healthpb.UnimplementedHealthServer{})

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go srv.Serve(lis)
	t.Cleanup(func() { srv.Stop() })

	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	defer conn.Close()

	client := healthpb.NewHealthClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err = client.Check(ctx, &healthpb.HealthCheckRequest{})
	require.Error(t, err)
}

func assertGRPCReachable(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		return
	}
	st, ok := status.FromError(err)
	if ok && st.Code() == codes.Unimplemented {
		return
	}
	t.Fatalf("expected reachable gRPC server, got: %v", err)
}
