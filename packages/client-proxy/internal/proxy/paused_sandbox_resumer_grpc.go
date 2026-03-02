package proxy

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	proxygrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/proxy"
)

type GrpcPausedSandboxResumerConfig struct {
	Address string

	TLSEnabled    bool
	TLSServerName string
	TLSCABase64   string

	TLSClientCertB64 string
	TLSClientKeyB64  string
}

type grpcPausedSandboxResumer struct {
	conn   *grpc.ClientConn
	client proxygrpc.SandboxServiceClient
}

func NewGrpcPausedSandboxResumer(config GrpcPausedSandboxResumerConfig) (PausedSandboxResumer, error) {
	// Client-proxy uses this gRPC client to trigger ResumeSandbox when needed.
	address := strings.TrimSpace(config.Address)
	if address == "" {
		return nil, fmt.Errorf("api grpc address is required")
	}

	transportCredentials, err := getTransportCredentials(config)
	if err != nil {
		return nil, fmt.Errorf("configure api grpc transport: %w", err)
	}

	conn, err := grpc.NewClient(address, grpc.WithTransportCredentials(transportCredentials))
	if err != nil {
		return nil, fmt.Errorf("create grpc client: %w", err)
	}

	return &grpcPausedSandboxResumer{
		conn:   conn,
		client: proxygrpc.NewSandboxServiceClient(conn),
	}, nil
}

func getTransportCredentials(config GrpcPausedSandboxResumerConfig) (credentials.TransportCredentials, error) {
	trimmedCA := strings.TrimSpace(config.TLSCABase64)
	trimmedClientCert := strings.TrimSpace(config.TLSClientCertB64)
	trimmedClientKey := strings.TrimSpace(config.TLSClientKeyB64)

	usesTLSSettings := trimmedCA != "" || trimmedClientCert != "" || trimmedClientKey != "" || strings.TrimSpace(config.TLSServerName) != ""

	if !config.TLSEnabled {
		if usesTLSSettings {
			return nil, fmt.Errorf("tls options provided while tls is disabled")
		}

		return insecure.NewCredentials(), nil
	}

	tlsConfig := &tls.Config{
		// Keep compatibility with TLS termination defaults.
		MinVersion: tls.VersionTLS12,
	}

	if serverName := strings.TrimSpace(config.TLSServerName); serverName != "" {
		tlsConfig.ServerName = serverName
	}

	if trimmedCA != "" {
		caPEM, err := base64.StdEncoding.DecodeString(trimmedCA)
		if err != nil {
			return nil, fmt.Errorf("decode tls ca: %w", err)
		}

		rootCAs := x509.NewCertPool()
		if ok := rootCAs.AppendCertsFromPEM(caPEM); !ok {
			return nil, fmt.Errorf("parse tls ca certificate")
		}
		tlsConfig.RootCAs = rootCAs
	}

	// mTLS is optional. If one part is present, both are required.
	if trimmedClientCert != "" || trimmedClientKey != "" {
		if trimmedClientCert == "" || trimmedClientKey == "" {
			return nil, fmt.Errorf("both tls client cert and key are required for mTLS")
		}

		clientCertPEM, err := base64.StdEncoding.DecodeString(trimmedClientCert)
		if err != nil {
			return nil, fmt.Errorf("decode tls client cert: %w", err)
		}

		clientKeyPEM, err := base64.StdEncoding.DecodeString(trimmedClientKey)
		if err != nil {
			return nil, fmt.Errorf("decode tls client key: %w", err)
		}

		clientCert, err := tls.X509KeyPair(clientCertPEM, clientKeyPEM)
		if err != nil {
			return nil, fmt.Errorf("load tls client key pair: %w", err)
		}

		tlsConfig.Certificates = []tls.Certificate{clientCert}
	}

	return credentials.NewTLS(tlsConfig), nil
}

func (c *grpcPausedSandboxResumer) Close(_ context.Context) error {
	return c.conn.Close()
}

func (c *grpcPausedSandboxResumer) Resume(ctx context.Context, sandboxId string, sandboxPort uint64, trafficAccessToken string, envdAccessToken string) (string, error) {
	ctx = metadata.AppendToOutgoingContext(ctx, proxygrpc.MetadataSandboxRequestPort, strconv.FormatUint(sandboxPort, 10))

	if trafficAccessToken != "" {
		ctx = metadata.AppendToOutgoingContext(ctx, proxygrpc.MetadataTrafficAccessToken, trafficAccessToken)
	}

	if envdAccessToken != "" {
		ctx = metadata.AppendToOutgoingContext(ctx, proxygrpc.MetadataEnvdAccessToken, envdAccessToken)
	}

	resp, err := c.client.ResumeSandbox(ctx, &proxygrpc.SandboxResumeRequest{
		SandboxId: sandboxId,
	})
	if err != nil {
		return "", fmt.Errorf("grpc resume: %w", err)
	}

	return strings.TrimSpace(resp.GetOrchestratorIp()), nil
}
