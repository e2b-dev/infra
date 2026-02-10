package clusters

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	nomadapi "github.com/hashicorp/nomad/api"
	"go.opentelemetry.io/otel"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/clusters/discovery"
	clickhouse "github.com/e2b-dev/infra/packages/clickhouse/pkg"
	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	infogrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator-info"
	api "github.com/e2b-dev/infra/packages/shared/pkg/http/edge"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/logs/loki"
	"github.com/e2b-dev/infra/packages/shared/pkg/machineinfo"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
	"github.com/e2b-dev/infra/packages/shared/pkg/synchronization"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

var tracer = otel.Tracer("github.com/e2b-dev/infra/packages/api/internal/clusters")

const (
	instancesSyncInterval = 5 * time.Second
	instancesSyncTimeout  = 5 * time.Second
)

type Cluster struct {
	ID            uuid.UUID
	SandboxDomain *string

	instances       *smap.Map[*Instance]
	synchronization *synchronization.Synchronize[discovery.Item, *Instance]
	resources       ClusterResource
}

var (
	ErrTemplateBuilderNotFound          = errors.New("template builder not found")
	ErrAvailableTemplateBuilderNotFound = errors.New("available template builder not found")
)

func newLocalCluster(
	ctx context.Context,
	tel *telemetry.Client,
	nomad *nomadapi.Client,
	clickhouse clickhouse.Clickhouse,
	queryLogsProvider *loki.LokiQueryProvider,
) (*Cluster, error) {
	clusterID := consts.LocalClusterID

	instances := smap.New[*Instance]()
	instanceCreation := func(ctx context.Context, item discovery.Item) (*Instance, error) {
		// For local cluster we are doing direct connection to instance IP and API port and without additional cluster auth.
		return newInstance(ctx, tel, nil, clusterID, item, fmt.Sprintf("%s:%d", item.LocalIPAddress, item.LocalInstanceApiPort), false)
	}

	storeDiscovery := discovery.NewLocalDiscovery(clusterID, nomad)
	store := instancesSyncStore{clusterID: clusterID, instances: instances, discovery: storeDiscovery, instanceCreation: instanceCreation}

	c := &Cluster{
		ID:            clusterID,
		SandboxDomain: nil,

		instances:       instances,
		resources:       newLocalClusterResourceProvider(clickhouse, queryLogsProvider, instances),
		synchronization: synchronization.NewSynchronize("cluster-instances", "Cluster instances", store),
	}

	// Periodically sync cluster instances
	go c.synchronization.Start(ctx, instancesSyncInterval, instancesSyncTimeout, true)

	return c, nil
}

func newRemoteCluster(
	ctx context.Context,
	tel *telemetry.Client,
	endpoint string,
	endpointTLS bool,
	secret string,
	clusterID uuid.UUID,
	sandboxDomain *string,
) (*Cluster, error) {
	scheme := "http"
	if endpointTLS {
		scheme = "https"
	}

	endpointBaseUrl := fmt.Sprintf("%s://%s", scheme, endpoint)

	httpClient, err := api.NewClientWithResponses(
		endpointBaseUrl,
		func(c *api.Client) error {
			c.RequestEditors = append(
				c.RequestEditors,
				func(_ context.Context, req *http.Request) error {
					req.Header.Set(consts.EdgeApiAuthHeader, secret)

					return nil
				},
			)

			return nil
		},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create http client: %w", err)
	}

	instances := smap.New[*Instance]()
	instanceCreation := func(ctx context.Context, item discovery.Item) (*Instance, error) {
		// For remote cluster we are doing connection to endpoint that works as gRPC proxy and handles auth and routing for us.
		auth := &instanceAuthorization{secret: secret, tls: endpointTLS, serviceInstanceID: item.InstanceID}

		return newInstance(ctx, tel, auth, clusterID, item, endpoint, endpointTLS)
	}

	storeDiscovery := discovery.NewRemoteServiceDiscovery(clusterID, httpClient)
	store := instancesSyncStore{clusterID: clusterID, instances: instances, instanceCreation: instanceCreation, discovery: storeDiscovery}

	c := &Cluster{
		ID:            clusterID,
		SandboxDomain: sandboxDomain,

		instances:       instances,
		resources:       newRemoteClusterResourceProvider(instances, httpClient),
		synchronization: synchronization.NewSynchronize("cluster-instances", "Cluster instances", store),
	}

	// Periodically sync cluster instances
	go c.synchronization.Start(ctx, instancesSyncInterval, instancesSyncTimeout, true)

	return c, nil
}

func (c *Cluster) Close(ctx context.Context) error {
	c.synchronization.Close()

	instances := c.instances.Items()
	wg := sync.WaitGroup{}

	for _, instance := range instances {
		wg.Go(func() {
			if closeErr := instance.Close(); closeErr != nil {
				logger.L().Error(ctx, "Failed to close cluster instance during cluster closing",
					zap.Error(closeErr),
					logger.WithClusterID(c.ID),
					logger.WithNodeID(instance.NodeID),
				)
			}
		})
	}

	// Wait for all instances to be closed
	wg.Wait()

	return nil
}

func (c *Cluster) GetTemplateBuilderByNodeID(nodeID string) (*Instance, error) {
	instance, found := c.instances.Get(nodeID)
	if !found {
		return nil, ErrTemplateBuilderNotFound
	}

	if info := instance.GetInfo(); info.Status == infogrpc.ServiceInfoStatus_Unhealthy || !info.IsBuilder {
		return nil, ErrTemplateBuilderNotFound
	}

	return instance, nil
}

func (c *Cluster) GetByServiceInstanceID(serviceInstanceID string) (*Instance, bool) {
	for _, instance := range c.instances.Items() {
		info := instance.GetInfo()
		if info.ServiceInstanceID == serviceInstanceID {
			return instance, true
		}
	}

	return nil, false
}

func (c *Cluster) GetAvailableTemplateBuilder(ctx context.Context, expectedInfo machineinfo.MachineInfo) (*Instance, error) {
	_, span := tracer.Start(ctx, "template-builder-get-available-instance")
	span.SetAttributes(telemetry.WithClusterID(c.ID))
	defer span.End()

	var instances []*Instance
	for _, instance := range c.instances.Items() {
		instances = append(instances, instance)
	}

	// Make sure we will always iterate in different order and when there is bigger amount of builders, we will not always pick the same one
	rand.Shuffle(len(instances), func(i, j int) { instances[i], instances[j] = instances[j], instances[i] })

	for _, instance := range instances {
		// Check availability and builder role
		if info := instance.GetInfo(); info.Status != infogrpc.ServiceInfoStatus_Healthy || !info.IsBuilder {
			continue
		}

		// Check machine compatibility
		if machineInfo := instance.GetMachineInfo(); expectedInfo.CPUModel != "" && !expectedInfo.IsCompatibleWith(machineInfo) {
			continue
		}

		return instance, nil
	}

	return nil, ErrAvailableTemplateBuilderNotFound
}

func (c *Cluster) GetOrchestrators() []*Instance {
	instances := make([]*Instance, 0)
	for _, i := range c.instances.Items() {
		if i.GetInfo().IsOrchestrator {
			instances = append(instances, i)
		}
	}

	return instances
}

func (c *Cluster) GetResources() ClusterResource {
	return c.resources
}
