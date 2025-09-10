package service_discovery

import (
	"context"
	"fmt"
	"time"

	nomadapi "github.com/hashicorp/nomad/api"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
)

const (
	nomadQueryRefreshInterval = 10 * time.Second
)

type NomadServiceDiscovery struct {
	logger  *zap.Logger
	entries *smap.Map[ServiceDiscoveryItem]
	client  *nomadapi.Client

	port   int
	filter string
}

func NewNomadServiceDiscovery(ctx context.Context, logger *zap.Logger, port int, nomadEndpoint string, nomadToken string, job string) (*NomadServiceDiscovery, error) {
	config := &nomadapi.Config{Address: nomadEndpoint, SecretID: nomadToken}
	client, err := nomadapi.NewClient(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create Nomad client: %w", err)
	}

	// We want to filter all jobs that are in running state and JobID contains (not equals as we are using suffixes sometimes)
	filter := fmt.Sprintf("ClientStatus == \"running\" and JobID contains \"%s\"", job)

	sd := &NomadServiceDiscovery{
		logger:  logger,
		client:  client,
		filter:  filter,
		port:    port,
		entries: smap.New[ServiceDiscoveryItem](),
	}

	go func() { sd.keepInSync(ctx) }()

	return sd, nil
}

func (sd *NomadServiceDiscovery) ListNodes(_ context.Context) ([]ServiceDiscoveryItem, error) {
	entries := sd.entries.Items()
	items := make([]ServiceDiscoveryItem, 0)

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
			sd.logger.Info("Stopping service discovery keep-in-sync")
			return
		case <-ticker.C:
			sd.sync(ctx)
		}
	}
}

func (sd *NomadServiceDiscovery) sync(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, nomadQueryRefreshInterval)
	defer cancel()

	options := &nomadapi.QueryOptions{
		Filter: sd.filter,

		// https://developer.hashicorp.com/nomad/api-docs/allocations#resources
		// Return allocation resources as part of the response
		Params: map[string]string{"resources": "true"},
	}

	results, _, err := sd.client.Allocations().List(options.WithContext(ctx))
	if err != nil {
		sd.logger.Error("Failed to list Nomad allocations in service discovery", zap.Error(err))
		return
	}

	found := make(map[string]string)
	for _, v := range results {
		if v.AllocatedResources == nil {
			sd.logger.Warn("No allocated resources found", zap.String("job", v.JobID), zap.String("alloc", v.ID))
			continue
		}

		nets := v.AllocatedResources.Shared.Networks
		if len(nets) == 0 {
			sd.logger.Warn("No allocation networks found", zap.String("job", v.JobID), zap.String("alloc", v.ID))
			continue
		}

		net := nets[0]
		key := fmt.Sprintf("%s:%d", net.IP, sd.port)
		item := ServiceDiscoveryItem{
			NodeIP:   net.IP,
			NodePort: sd.port,
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
