package edge

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/metadata"

	grpclient "github.com/e2b-dev/infra/packages/api/internal/grpc"
	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	orchestratorgrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
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

	instances       *smap.Map[*ClusterInstance]
	synchronization *synchronization.Synchronize[api.ClusterOrchestratorNode, *ClusterInstance]
	tracer          trace.Tracer
	SandboxDomain   *string
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

func NewCluster(
	ctx context.Context,
	tracer trace.Tracer,
	tel *telemetry.Client,
	endpoint string,
	endpointTLS bool,
	secret string,
	clusterID uuid.UUID,
	sandboxDomain *string,
) (*Cluster, error) {
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
		ID:            clusterID,
		SandboxDomain: sandboxDomain,

		instances:  smap.New[*ClusterInstance](),
		tracer:     tracer,
		httpClient: httpClient,
		grpcClient: grpcClient,
	}

	store := clusterSynchronizationStore{cluster: c}
	c.synchronization = synchronization.NewSynchronize(tracer, "cluster-instances", "Cluster instances", store)

	// periodically sync cluster instances
	go c.startSync(context.WithoutCancel(ctx))

	return c, nil
}

func (c *Cluster) Close() error {
	c.synchronization.Close()
	err := c.grpcClient.Close()
	return err
}

func (c *Cluster) GetTemplateBuilderByNodeID(nodeID string) (*ClusterInstance, error) {
	instance, found := c.instances.Get(nodeID)
	if !found {
		return nil, ErrTemplateBuilderNotFound
	}

	if instance.GetStatus() == infogrpc.ServiceInfoStatus_Unhealthy || !instance.IsBuilder() {
		return nil, ErrTemplateBuilderNotFound
	}

	return instance, nil
}

func (c *Cluster) GetByServiceInstanceID(serviceInstanceID string) (*ClusterInstance, bool) {
	for _, instance := range c.instances.Items() {
		if instance.ServiceInstanceID == serviceInstanceID {
			return instance, true
		}
	}

	return nil, false
}

func (c *Cluster) GetAvailableTemplateBuilder(ctx context.Context) (*ClusterInstance, error) {
	_, span := c.tracer.Start(ctx, "template-builder-get-available-instance")
	span.SetAttributes(telemetry.WithClusterID(c.ID))
	defer span.End()

	for _, instance := range c.instances.Items() {
		if instance.GetStatus() != infogrpc.ServiceInfoStatus_Healthy {
			continue
		}

		if !instance.IsBuilder() {
			continue
		}

		return instance, nil
	}

	return nil, ErrAvailableTemplateBuilderNotFound
}

func (c *Cluster) GetGRPC(serviceInstanceID string) *ClusterGRPC {
	return &ClusterGRPC{c.grpcClient, metadata.New(map[string]string{consts.EdgeRpcServiceInstanceIDHeader: serviceInstanceID})}
}

func (c *Cluster) GetHTTP(nodeID string) *ClusterHTTP {
	return &ClusterHTTP{c.httpClient, nodeID}
}

func (c *Cluster) GetOrchestrators() []*ClusterInstance {
	instances := make([]*ClusterInstance, 0)
	for _, i := range c.instances.Items() {
		if i.IsOrchestrator() {
			instances = append(instances, i)
		}
	}

	return instances
}

func (c *Cluster) GetHttpClient() *api.ClientWithResponses {
	return c.httpClient
}

func (c *Cluster) RegisterSandboxInCatalog(ctx context.Context, serviceInstanceID string, sandboxStartTime time.Time, sandboxConfig *orchestratorgrpc.SandboxConfig) error {
	body := api.V1SandboxCatalogCreateJSONRequestBody{
		OrchestratorID: serviceInstanceID,

		ExecutionID:      sandboxConfig.ExecutionId,
		SandboxID:        sandboxConfig.SandboxId,
		SandboxMaxLength: sandboxConfig.MaxSandboxLength,
		SandboxStartTime: sandboxStartTime,
	}

	rsp, err := c.httpClient.V1SandboxCatalogCreate(ctx, body)
	if err != nil {
		return fmt.Errorf("failed to register sandbox in catalog: %w", err)
	}
	defer rsp.Body.Close()

	return nil
}

func (c *Cluster) RemoveSandboxFromCatalog(ctx context.Context, sandboxID string, executionID string) error {
	body := api.V1SandboxCatalogDeleteJSONRequestBody{
		SandboxID:   sandboxID,
		ExecutionID: executionID,
	}

	rsp, err := c.httpClient.V1SandboxCatalogDelete(ctx, body)
	if err != nil {
		return fmt.Errorf("failed to remove sandbox from catalog: %w", err)
	}
	defer rsp.Body.Close()

	return nil
}
