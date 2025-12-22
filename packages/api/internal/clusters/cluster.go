package clusters

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"google.golang.org/grpc/metadata"

	grpclient "github.com/e2b-dev/infra/packages/api/internal/grpc"
	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	infogrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator-info"
	api "github.com/e2b-dev/infra/packages/shared/pkg/http/edge"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
	"github.com/e2b-dev/infra/packages/shared/pkg/synchronization"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

var tracer = otel.Tracer("github.com/e2b-dev/infra/packages/api/internal/clusters")

type Cluster struct {
	ID            uuid.UUID
	SandboxDomain *string

	httpClient      *api.ClientWithResponses
	grpcClient      *grpclient.GRPCClient
	instances       *smap.Map[*Instance]
	synchronization *synchronization.Synchronize[api.ClusterOrchestratorNode, *Instance]
	resources       ClusterResource
}

type ClusterGRPC struct {
	Client   *grpclient.GRPCClient
	Metadata metadata.MD
}

type ClusterHTTP struct {
	Client *api.ClientWithResponses
}

var (
	ErrTemplateBuilderNotFound          = errors.New("template builder not found")
	ErrAvailableTemplateBuilderNotFound = errors.New("available template builder not found")
)

func NewCluster(ctx context.Context, tel *telemetry.Client, endpoint string, endpointTLS bool, secret string, clusterID uuid.UUID, sandboxDomain *string) (*Cluster, error) {
	clientAuthMiddleware := func(c *api.Client) error {
		c.RequestEditors = append(
			c.RequestEditors,
			func(_ context.Context, req *http.Request) error {
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

	instances := smap.New[*Instance]()

	c := &Cluster{
		ID:            clusterID,
		SandboxDomain: sandboxDomain,

		resources:  newRemoteClusterResourceProvider(instances, httpClient),
		instances:  instances,
		httpClient: httpClient,
		grpcClient: grpcClient,
	}

	store := clusterSynchronizationStore{cluster: c}
	c.synchronization = synchronization.NewSynchronize("cluster-instances", "Cluster instances", store)

	// periodically sync cluster instances
	go c.startSync(ctx)

	return c, nil
}

func (c *Cluster) Close() error {
	c.synchronization.Close()
	err := c.grpcClient.Close()

	return err
}

func (c *Cluster) GetTemplateBuilderByNodeID(nodeID string) (*Instance, error) {
	instance, found := c.instances.Get(nodeID)
	if !found {
		return nil, ErrTemplateBuilderNotFound
	}

	if instance.GetStatus() == infogrpc.ServiceInfoStatus_Unhealthy || !instance.IsBuilder() {
		return nil, ErrTemplateBuilderNotFound
	}

	return instance, nil
}

func (c *Cluster) GetByServiceInstanceID(serviceInstanceID string) (*Instance, bool) {
	for _, instance := range c.instances.Items() {
		if instance.InstanceID == serviceInstanceID {
			return instance, true
		}
	}

	return nil, false
}

func (c *Cluster) GetAvailableTemplateBuilder(ctx context.Context) (*Instance, error) {
	_, span := tracer.Start(ctx, "template-builder-get-available-instance")
	span.SetAttributes(telemetry.WithClusterID(c.ID))
	defer span.End()

	var instances []*Instance
	for _, instance := range c.instances.Items() {
		instances = append(instances, instance)
	}

	// Make sure we will always iterate in different order and when there is bigger amount of builders, we will not always pick the same one
	rand.Shuffle(len(instances), func(i, j int) {
		instances[i], instances[j] = instances[j], instances[i]
	})

	for _, instance := range instances {
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

func (c *Cluster) GetHTTP() *ClusterHTTP {
	return &ClusterHTTP{c.httpClient}
}

func (c *Cluster) GetOrchestrators() []*Instance {
	instances := make([]*Instance, 0)
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

func (c *Cluster) GetResources() ClusterResource {
	return c.resources
}
