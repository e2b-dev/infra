package edge

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/metadata"

	grpclient "github.com/e2b-dev/infra/packages/api/internal/grpc"
	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	infogrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator-info"
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
				req.Header.Set(consts.EdgeApiAuthHeader, secret)
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
	grpcClient, err := createClusterClient(tel, grpcAuthorization, endpoint, endpointTls)
	if err != nil {
		ctxCancel()
		return nil, fmt.Errorf("failed to create grpc client: %w", err)
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
	return c.grpcClient, metadata.New(map[string]string{consts.EdgeRpcNodeHeader: nodeID})
}

func (c *Cluster) GetHttpClient() *api.ClientWithResponses {
	return c.httpClient
}
