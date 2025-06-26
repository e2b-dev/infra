package edge

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/metadata"

	grpclient "github.com/e2b-dev/infra/packages/api/internal/grpc"
	infogrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator-info"
	templatemanagergrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	api "github.com/e2b-dev/infra/packages/shared/pkg/http/edge"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

type Cluster struct {
	ID uuid.UUID

	httpClient *api.ClientWithResponses
	grpcClient *grpclient.GRPCClient

	nodes  *smap.Map[*ClusterNode]
	tracer trace.Tracer

	ctx       context.Context
	ctxCancel context.CancelFunc
}

const (
	edgeApiAuthHeader = "X-API-Key"
	edgeRpcAuthHeader = "authorization"
	edgeRpcNodeHeader = "node-id"
)

var (
	ErrTemplateBuilderNotFound          = errors.New("template builder not found")
	ErrAvailableTemplateBuilderNotFound = errors.New("available template builder not found")
)

func NewCluster(ctx context.Context, tracer trace.Tracer, tel *telemetry.Client, endpoint string, endpointTls bool, secret string, id uuid.UUID) (*Cluster, error) {
	ctx, ctxCancel := context.WithCancel(ctx)

	clientAuthMiddleware := func(c *api.Client) error {
		c.RequestEditors = append(
			c.RequestEditors,
			func(ctx context.Context, req *http.Request) error {
				req.Header.Set(edgeApiAuthHeader, secret)
				return nil
			},
		)
		return nil
	}

	// generate the full endpoint URL
	var endpointBaseUrl string
	if endpointTls {
		endpointBaseUrl = fmt.Sprintf("https://%s", endpoint)
	} else {
		endpointBaseUrl = fmt.Sprintf("http://%s", endpoint)
	}

	httpClient, err := api.NewClientWithResponses(endpointBaseUrl, clientAuthMiddleware)
	if err != nil {
		ctxCancel()
		return nil, fmt.Errorf("failed to create http client: %w", err)
	}

	grpcAuthorization := clientAuthorization{secret: secret, tls: endpointTls}
	grpcOptions := []grpc.DialOption{
		grpc.WithPerRPCCredentials(grpcAuthorization),
		grpc.WithStatsHandler(
			otelgrpc.NewClientHandler(
				otelgrpc.WithTracerProvider(tel.TracerProvider),
				otelgrpc.WithMeterProvider(tel.MeterProvider),
			),
		),
		grpc.WithKeepaliveParams(
			keepalive.ClientParameters{
				Time:                10 * time.Second, // Send ping every 10s
				Timeout:             2 * time.Second,  // Wait 2s for response
				PermitWithoutStream: true,
			},
		),
	}

	if endpointTls {
		// (2025-06) AWS ALB with TLS termination is using TLS 1.2 as default so this is why we are not using TLS 1.3+ here
		cred := credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS12})
		grpcOptions = append(grpcOptions, grpc.WithAuthority(endpoint), grpc.WithTransportCredentials(cred))
	} else {
		grpcOptions = append(grpcOptions, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}

	conn, err := grpc.NewClient(endpoint, grpcOptions...)
	if err != nil {
		ctxCancel()
		return nil, fmt.Errorf("failed to create grpc client: %w", err)
	}

	grpcClient := &grpclient.GRPCClient{
		Sandbox:    nil,
		Info:       infogrpc.NewInfoServiceClient(conn),
		Template:   templatemanagergrpc.NewTemplateServiceClient(conn),
		Connection: conn,
	}

	c := &Cluster{
		ID: id,

		ctx:       ctx,
		ctxCancel: ctxCancel,

		nodes:      smap.New[*ClusterNode](),
		tracer:     tracer,
		httpClient: httpClient,
		grpcClient: grpcClient,
	}

	// periodically sync cluster nodes
	go c.syncBackground()

	return c, nil
}

func (c *Cluster) Close() error {
	err := c.grpcClient.Close()
	c.ctxCancel()
	return err
}

func (c *Cluster) GetTemplateBuilderById(nodeID string) (*ClusterNode, error) {
	node, found := c.nodes.Get(nodeID)
	if !found {
		return nil, ErrTemplateBuilderNotFound
	}

	if node.GetStatus() == infogrpc.ServiceInfoStatus_OrchestratorUnhealthy || !node.IsBuilderNode() {
		return nil, ErrTemplateBuilderNotFound
	}

	return node, nil
}

func (c *Cluster) GetAvailableTemplateBuilder() (*ClusterNode, error) {
	_, span := c.tracer.Start(c.ctx, "template-builder-get-available-node")
	span.SetAttributes(telemetry.WithClusterID(c.ID))
	defer span.End()

	for _, node := range c.nodes.Items() {
		// we don't want to place new builds to not healthy nodes
		if node.GetStatus() != infogrpc.ServiceInfoStatus_OrchestratorHealthy {
			continue
		}

		// we want to use only template builders
		if !node.IsBuilderNode() {
			continue
		}

		return node, nil
	}

	return nil, ErrAvailableTemplateBuilderNotFound
}

func (c *Cluster) GetGrpcClient(nodeID string) (*grpclient.GRPCClient, metadata.MD) {
	return c.grpcClient, metadata.New(map[string]string{edgeRpcNodeHeader: nodeID})
}

func (c *Cluster) GetHttpClient() *api.ClientWithResponses {
	return c.httpClient
}
