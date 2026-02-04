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

	"github.com/e2b-dev/infra/packages/api/internal/cfg"
	"github.com/e2b-dev/infra/packages/api/internal/clusters/discovery"
	clickhouse "github.com/e2b-dev/infra/packages/clickhouse/pkg"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
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

func NewCluster(
	clusterID uuid.UUID,
	domain *string,
	sandboxes *smap.Map[*Instance],
	synchronization *synchronization.Synchronize[discovery.Item, *Instance],
	resources ClusterResource,
) *Cluster {
	return &Cluster{
		ID:              clusterID,
		SandboxDomain:   domain,
		instances:       sandboxes,
		synchronization: synchronization,
		resources:       resources,
	}
}

func newLocalCluster(
	ctx context.Context,
	tel *telemetry.Client,
	nomad *nomadapi.Client,
	clickhouse clickhouse.Clickhouse,
	queryLogsProvider *loki.LokiQueryProvider,
	config cfg.Config,
) (*Cluster, error) {
	clusterID := consts.LocalClusterID

	instances := smap.New[*Instance]()
	instanceCreation := func(ctx context.Context, item discovery.Item) (*Instance, error) {
		// For local cluster we are doing direct connection to instance IP and API port and without additional cluster auth.
		return newInstance(ctx, tel, nil, clusterID, item, fmt.Sprintf("%s:%d", item.LocalIPAddress, item.LocalInstanceApiPort), false)
	}

	storeDiscovery := discovery.NewLocalDiscovery(clusterID, nomad)
	store := instancesSyncStore{clusterID: clusterID, instances: instances, discovery: storeDiscovery, instanceCreation: instanceCreation}

	c := NewCluster(
		clusterID,
		nil,
		instances,
		synchronization.NewSynchronize("cluster-instances", "Cluster instances", store),
		newLocalClusterResourceProvider(clickhouse, queryLogsProvider, instances, config),
	)

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

	c := NewCluster(
		clusterID,
		sandboxDomain,
		instances,
		synchronization.NewSynchronize("cluster-instances", "Cluster instances", store),
		newRemoteClusterResourceProvider(instances, httpClient),
	)

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

func (c *Cluster) getRandomInstance(isMatch func(InstanceInfo, machineinfo.MachineInfo) bool) (*Instance, bool) {
	var instances []*Instance
	for _, instance := range c.instances.Items() {
		instances = append(instances, instance)
	}

	// Make sure we will always iterate in different order and when there is bigger amount of builders, we will not always pick the same one
	rand.Shuffle(len(instances), func(i, j int) { instances[i], instances[j] = instances[j], instances[i] })

	for _, instance := range instances {
		if isMatch(instance.GetInfo(), instance.GetMachineInfo()) {
			return instance, true
		}
	}

	return nil, false
}

func (c *Cluster) GetAvailableTemplateBuilder(ctx context.Context, expectedInfo machineinfo.MachineInfo) (*Instance, error) {
	_, span := tracer.Start(ctx, "template-builder-get-available-instance")
	span.SetAttributes(telemetry.WithClusterID(c.ID))
	defer span.End()

	instance, ok := c.getRandomInstance(func(info InstanceInfo, machineInfo machineinfo.MachineInfo) bool {
		// Check availability and builder role
		if info.Status != infogrpc.ServiceInfoStatus_Healthy || !info.IsBuilder {
			return false
		}

		// Check machine compatibility
		if expectedInfo.CPUModel != "" && !expectedInfo.IsCompatibleWith(machineInfo) {
			return false
		}

		return true
	})
	if !ok {
		return nil, ErrAvailableTemplateBuilderNotFound
	}

	return instance, nil
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

var ErrNoOrchestratorFound = errors.New("no orchestrator found")

func (c *Cluster) DeleteVolume(ctx context.Context, volume queries.Volume) error {
	instance, ok := c.getRandomInstance(func(instance InstanceInfo, _ machineinfo.MachineInfo) bool {
		return instance.IsOrchestrator
	})

	if !ok {
		return ErrNoOrchestratorFound
	}

	if _, err := instance.client.Volumes.Delete(ctx, &orchestrator.VolumeDeleteRequest{
		VolumeId:   volume.ID.String(),
		VolumeType: volume.VolumeType,
		VolumeName: volume.Name,
	}); err != nil {
		return fmt.Errorf("failed to delete volume: %w", err)
	}

	return nil
}
