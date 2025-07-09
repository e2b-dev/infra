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
	"github.com/e2b-dev/infra/packages/shared/pkg/synchronization"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

type Cluster struct {
	ID uuid.UUID

	httpClient *api.ClientWithResponses
	grpcClient *grpclient.GRPCClient

	nodes           *smap.Map[*ClusterNode]
	synchronization *synchronization.Synchronize[api.ClusterOrchestratorNode, *ClusterNode]
	tracer          trace.Tracer
}

type ClusterGRPC struct {
	Client   *grpclient.GRPCClient
	Metadata metadata.MD
}

type ClusterHTTP struct {
	Client *api.ClientWithResponses
	NodeID string
}

var (
	ErrTemplateBuilderNotFound          = errors.New("template builder not found")
	ErrAvailableTemplateBuilderNotFound = errors.New("available template builder not found")
)

func NewCluster(tracer trace.Tracer, tel *telemetry.Client, endpoint string, endpointTLS bool, secret string, clusterID uuid.UUID) (*Cluster, error) {
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
	if endpointTLS {
		endpointBaseUrl = fmt.Sprintf("https://%s", endpoint)
	} else {
		endpointBaseUrl = fmt.Sprintf("http://%s", endpoint)
	}

	httpClient, err := api.NewClientWithResponses(endpointBaseUrl, clientAuthMiddleware)
	if err != nil {
		return nil, fmt.Errorf("failed to create http client: %w", err)
	}

	grpcAuthorization := clientAuthorization{secret: secret, tls: endpointTLS}
	grpcClient, err := createClusterClient(tel, grpcAuthorization, endpoint, endpointTLS)
	if err != nil {
		return nil, fmt.Errorf("failed to create grpc client: %w", err)
	}

	c := &Cluster{
		ID: clusterID,

		nodes:      smap.New[*ClusterNode](),
		tracer:     tracer,
		httpClient: httpClient,
		grpcClient: grpcClient,
	}

	store := clusterSynchronizationStore{cluster: c}
	c.synchronization = synchronization.NewSynchronize(tracer, "cluster-nodes", "Cluster nodes", store)

	// periodically sync cluster nodes
	go c.startSync()

	return c, nil
}

func (c *Cluster) Close() error {
	c.synchronization.Close()
	err := c.grpcClient.Close()
	return err
}

func (c *Cluster) GetTemplateBuilderByID(nodeID string) (*ClusterNode, error) {
	node, found := c.nodes.Get(nodeID)
	if !found {
		return nil, ErrTemplateBuilderNotFound
	}

	if node.GetStatus() == infogrpc.ServiceInfoStatus_OrchestratorUnhealthy || !node.IsBuilderNode() {
		return nil, ErrTemplateBuilderNotFound
	}

	return node, nil
}

func (c *Cluster) GetAvailableTemplateBuilder(ctx context.Context) (*ClusterNode, error) {
	_, span := c.tracer.Start(ctx, "template-builder-get-available-node")
	span.SetAttributes(telemetry.WithClusterID(c.ID))
	defer span.End()

	for _, node := range c.nodes.Items() {
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

func (c *Cluster) GetGRPC(serviceInstanceID string) *ClusterGRPC {
	return &ClusterGRPC{c.grpcClient, metadata.New(map[string]string{consts.EdgeRpcServiceInstanceIDHeader: serviceInstanceID})}
}

func (c *Cluster) GetHTTP(nodeID string) *ClusterHTTP {
	return &ClusterHTTP{c.httpClient, nodeID}
}
