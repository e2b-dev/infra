package service_discovery

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

type NomadServiceDiscovery struct {
	logger  logger.Logger
	entries *smap.Map[DiscoveredInstance]
	client  *nomadapi.Client

	port uint16
}

func NewNomadServiceDiscovery(ctx context.Context, logger logger.Logger, port uint16, nomadEndpoint string, nomadToken string) (*NomadServiceDiscovery, error) {
	config := &nomadapi.Config{Address: nomadEndpoint, SecretID: nomadToken}
	client, err := nomadapi.NewClient(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create Nomad client: %w", err)
	}

	sd := &NomadServiceDiscovery{
		logger:  logger,
		client:  client,
		port:    port,
		entries: smap.New[DiscoveredInstance](),
	}

	go func() { sd.keepInSync(ctx) }()

	return sd, nil
}

func (sd *NomadServiceDiscovery) ListInstances(_ context.Context) ([]DiscoveredInstance, error) {
	entries := sd.entries.Items()
	items := make([]DiscoveredInstance, 0)

	for _, item := range entries {
		items = append(items, item)
	}

	return items, nil
}

func (sd *NomadServiceDiscovery) keepInSync(ctx context.Context) {
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

func (sd *NomadServiceDiscovery) sync(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, nomadQueryRefreshInterval)
	defer cancel()

	alloc, err := discovery.ListOrchestratorAndTemplateBuilderAllocations(ctx, sd.client)
	if err != nil {
		sd.logger.Error(ctx, "Failed to list orchestrator and template builders", zap.Error(err))

		return
	}

	found := make(map[string]string, len(alloc))
	for _, v := range alloc {
		key := fmt.Sprintf("%s:%d", v.AllocationIP, sd.port)
		item := DiscoveredInstance{
			InstanceIPAddress: v.AllocationIP,
			InstancePort:      sd.port,
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
