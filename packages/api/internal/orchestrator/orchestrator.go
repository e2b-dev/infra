package orchestrator

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/e2b-dev/infra/packages/api/internal/cache/instance"
)

type Orchestrator struct {
	clients map[string]*GRPCClient
}

func New() (*Orchestrator, error) {
	return &Orchestrator{
		clients: map[string]*GRPCClient{},
	}, nil
}

func (o *Orchestrator) Close() error {
	for _, client := range o.clients {
		err := client.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func (o *Orchestrator) GetClient(nodeID string) (*GRPCClient, error) {
	if ok := o.clients[nodeID]; ok != nil {
		return ok, nil
	}
	client, err := NewClient(nodeID)
	if err != nil {
		return nil, err
	}

	o.clients[nodeID] = client
	return client, nil
}

// KeepInSync the cache with the actual instances in Orchestrator to handle instances that died.
func (o *Orchestrator) KeepInSync(ctx context.Context, instanceCache *instance.InstanceCache) {
	for {
		time.Sleep(instance.CacheSyncTime)

		for nodeID := range o.clients {
			activeInstances, err := o.GetInstances(ctx, nodeID)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error loading current sandboxes\n: %v", err)
			} else {
				instanceCache.Sync(activeInstances, nodeID)
			}
		}
		instanceCache.SendAnalyticsEvent()
	}
}
