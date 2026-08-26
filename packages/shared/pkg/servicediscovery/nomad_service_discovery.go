package servicediscovery

import (
	"context"
	"fmt"
	"time"

	nomadapi "github.com/hashicorp/nomad/api"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/clusters/discovery"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
)

const (
	nomadQueryRefreshInterval = 10 * time.Second
)

type NodePlaneInstance struct {
	logger  logger.Logger
	entries *smap.Map[Instance]
	client  *nomadapi.Client
	cancel  func()

	port uint16
}

func NewNodePlaneInstance(logger logger.Logger, port uint16, nomadEndpoint string, nomadToken string) (*NodePlaneInstance, error) {
	config := &nomadapi.Config{Address: nomadEndpoint, SecretID: nomadToken}
	client, err := nomadapi.NewClient(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create Nomad client: %w", err)
	}

	sd := &NodePlaneInstance{
		logger:  logger,
		client:  client,
		port:    port,
		entries: smap.New[Instance](),
		cancel:  func() {},
	}

	return sd, nil
}

func (sd *NodePlaneInstance) Start(ctx context.Context) {
	ctx, sd.cancel = context.WithCancel(ctx)

	go sd.keepInSync(ctx)
}

func (sd *NodePlaneInstance) Stop(_ context.Context) {
	sd.cancel()
}

func (sd *NodePlaneInstance) ListInstances(_ context.Context) ([]Instance, error) {
	entries := sd.entries.Items()
	items := make([]Instance, 0)

	for _, item := range entries {
		items = append(items, item)
	}

	return items, nil
}

func (sd *NodePlaneInstance) keepInSync(ctx context.Context) {
	// Run the first sync immediately
	sd.sync(ctx)

	ticker := time.NewTicker(nomadQueryRefreshInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			sd.logger.Info(ctx, "Stopping service discovery keep-in-sync")

			return
		case <-ticker.C:
			sd.sync(ctx)
		}
	}
}

func (sd *NodePlaneInstance) sync(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, nomadQueryRefreshInterval)
	defer cancel()

	alloc, err := discovery.ListOrchestratorAndTemplateBuilderAllocations(ctx, sd.client, discovery.FilterTemplateBuildersAndOrchestrators)
	if err != nil {
		sd.logger.Error(ctx, "Failed to list orchestrator and template builders", zap.Error(err))

		return
	}

	found := make(map[string]string, len(alloc))
	for _, v := range alloc {
		key := fmt.Sprintf("%s:%d", v.AllocationIP, sd.port)
		item := Instance{
			WorkloadID: key,
			IPAddress:  v.AllocationIP,
			Port:       sd.port,
		}

		sd.entries.Insert(key, item)
		found[key] = key
	}

	// Remove entries that are no longer in Nomad API response
	for key := range sd.entries.Items() {
		if _, ok := found[key]; !ok {
			sd.entries.Remove(key)
		}
	}
}
