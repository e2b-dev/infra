package proxy

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	e2bgrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc"
	proxygrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/proxy"
)

type grpcPausedSandboxResumer struct {
	conn             *grpc.ClientConn
	client           proxygrpc.SandboxServiceClient
	oauthTokenSource oauth2.TokenSource
}

type GrpcOAuthConfig struct {
	ClientID     string
	ClientSecret string
	TokenURL     string
}

func (c GrpcOAuthConfig) Enabled() bool {
	return strings.TrimSpace(c.ClientID) != "" ||
		strings.TrimSpace(c.ClientSecret) != "" ||
		strings.TrimSpace(c.TokenURL) != ""
}

func (c GrpcOAuthConfig) tokenSource(ctx context.Context) (oauth2.TokenSource, error) {
	if !c.Enabled() {
		return nil, nil
	}

	if strings.TrimSpace(c.ClientID) == "" ||
		strings.TrimSpace(c.ClientSecret) == "" ||
		strings.TrimSpace(c.TokenURL) == "" {
		return nil, errors.New("api grpc OAuth client ID, client secret, and token URL are required when OAuth is configured")
	}

	oauthConfig := clientcredentials.Config{
		ClientID:     strings.TrimSpace(c.ClientID),
		ClientSecret: strings.TrimSpace(c.ClientSecret),
		TokenURL:     strings.TrimSpace(c.TokenURL),
	}

	return oauthConfig.TokenSource(ctx), nil
}

func apiGrpcAddressUsesTLS(address string) bool {
	address = strings.TrimSpace(address)

	host, _, err := net.SplitHostPort(address)
	if err != nil {
		host = address
	}
	host = strings.Trim(strings.TrimSpace(host), "[]")

	if host == "" || host == "localhost" || strings.HasSuffix(host, ".service.consul") {
		return false
	}
	if net.ParseIP(host) != nil {
		return false
	}

	return true
}

func NewGrpcPausedSandboxResumer(address string, oauthConfig GrpcOAuthConfig) (PausedSandboxResumer, error) {
	if strings.TrimSpace(address) == "" {
		return nil, errors.New("api grpc address is required")
	}

	oauthTokenSource, err := oauthConfig.tokenSource(context.Background())
	if err != nil {
		return nil, err
	}

	creds := insecure.NewCredentials()
	if apiGrpcAddressUsesTLS(address) {
		creds = credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS12})
	}

	conn, err := grpc.NewClient(
		address,
		grpc.WithTransportCredentials(creds),
		grpc.WithStatsHandler(otelgrpc.NewClientHandler()),
	)
	if err != nil {
		return nil, fmt.Errorf("create grpc client: %w", err)
	}

	return &grpcPausedSandboxResumer{
		conn:             conn,
		client:           proxygrpc.NewSandboxServiceClient(conn),
		oauthTokenSource: oauthTokenSource,
	}, nil
}

func (c *grpcPausedSandboxResumer) Init(ctx context.Context) {
	e2bgrpc.ObserveConnection(ctx, c.conn, "api-resumer")
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

	if c.oauthTokenSource != nil {
		token, tokenErr := c.oauthTokenSource.Token()
		if tokenErr != nil {
			return "", fmt.Errorf("get api grpc OAuth token: %w", tokenErr)
		}

		ctx = metadata.AppendToOutgoingContext(ctx, proxygrpc.MetadataAuthorization, "Bearer "+token.AccessToken)
	}

	resp, err := c.client.ResumeSandbox(ctx, &proxygrpc.SandboxResumeRequest{
		SandboxId: sandboxId,
	})
	if err != nil {
		return "", fmt.Errorf("grpc resume: %w", err)
	}

	return strings.TrimSpace(resp.GetOrchestratorIp()), nil
}
