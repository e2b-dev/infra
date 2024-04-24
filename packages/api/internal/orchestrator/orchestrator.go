package orchestrator

import (
	"context"
	"fmt"
	"os"
	"time"

	consulapi "github.com/hashicorp/consul/api"
	nomadapi "github.com/hashicorp/nomad/api"

	"github.com/e2b-dev/infra/packages/api/internal/cache/instance"
	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
)

type Orchestrator struct {
	nomadClient  *nomadapi.Client
	consulClient *consulapi.Client
	clients      map[string]*GRPCClient
	nodeToHost   map[string]string
}

func New(nomadClient *nomadapi.Client, consulClient *consulapi.Client) (*Orchestrator, error) {
	return &Orchestrator{
		nomadClient:  nomadClient,
		consulClient: consulClient,
		clients:      map[string]*GRPCClient{},
		nodeToHost:   map[string]string{},
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

func (o *Orchestrator) GetClient(host string) (*GRPCClient, error) {
	if ok := o.clients[host]; ok != nil {
		return ok, nil
	}
	client, err := NewClient(host)
	if err != nil {
		return nil, err
	}

	o.clients[host] = client
	return client, nil
}

func (o *Orchestrator) GetHost(nodeID string) (string, error) {
	if host, ok := o.nodeToHost[nodeID]; ok {
		return host, nil
	}

	nodes, err := o.ListNodes()
	if err != nil {
		return "", err
	}

	for _, node := range nodes {
		if o.getIdFromNode(node) == nodeID {
			o.nodeToHost[nodeID] = node.Address
			return node.Address, nil
		}
	}

	return "", fmt.Errorf("node %s not found", nodeID)
}

func (o *Orchestrator) GetClientByNodeID(nodeID string) (*GRPCClient, error) {
	host, err := o.GetHost(nodeID)
	if err != nil {
		return nil, err
	}

	return o.GetClient(host)
}

func (o *Orchestrator) ListNodes() ([]*nomadapi.NodeListStub, error) {
	nodes, _, err := o.nomadClient.Nodes().List(&nomadapi.QueryOptions{Filter: "Status == \"ready\""})
	if err != nil {
		return nil, err
	}

	return nodes, nil
}

// KeepInSync the cache with the actual instances in Orchestrator to handle instances that died.
func (o *Orchestrator) KeepInSync(ctx context.Context, instanceCache *instance.InstanceCache) {
	for {
		time.Sleep(instance.CacheSyncTime)

		// TODO: We can use host directly instead of nodeID
		for nodeID := range o.nodeToHost {
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

// TODO: load all hosts?
// InitialSync loads already running instances from Orchestrator
func (o *Orchestrator) InitialSync(ctx context.Context) (instances []*instance.InstanceInfo, err error) {
	nodes, err := o.ListNodes()
	if err != nil {
		return instances, err
	}

	for _, node := range nodes {
		activeInstances, instancesErr := o.GetInstances(ctx, o.getIdFromNode(node))
		if instancesErr != nil {
			return nil, instancesErr
		}

		instances = append(instances, activeInstances...)
	}

	return instances, nil
}

func (o *Orchestrator) getIdFromNode(node *nomadapi.NodeListStub) string {
	return node.ID[:consts.NodeIDLength]
}
