package orchestrator

import (
	"context"
	"fmt"
	"log"
	"time"

	nomadapi "github.com/hashicorp/nomad/api"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/cache/instance"
	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
)

type Node struct {
	ID       string
	CPUUsage int64
	RamUsage int64
}

type Orchestrator struct {
	nomadClient *nomadapi.Client
	clients     map[string]*GRPCClient
	nodeToHost  map[string]string
	nodes       map[string]*Node
}

func New(nomadClient *nomadapi.Client) (*Orchestrator, error) {
	return &Orchestrator{
		nomadClient: nomadClient,
		clients:     map[string]*GRPCClient{},
		nodeToHost:  map[string]string{},
		nodes:       map[string]*Node{},
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

func (o *Orchestrator) GetNodeById(nodeID string) *Node {
	return o.nodes[nodeID]
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
func (o *Orchestrator) KeepInSync(ctx context.Context, instanceCache *instance.InstanceCache, logger *zap.SugaredLogger) {
	for {
		time.Sleep(instance.CacheSyncTime)
		nodes, err := o.ListNodes()
		if err != nil {
			log.Printf("Error loading nodes\n: %v", err)
			continue
		}

		nodeIds := make([]string, 0, len(nodes))
		for _, node := range nodes {
			nodeIds = append(nodeIds, o.getIdFromNode(node))
			if _, ok := o.nodes[o.getIdFromNode(node)]; !ok {
				_, err := o.connectToNode(ctx, node)
				if err != nil {
					log.Printf("Error connecting to node\n: %v", err)
				}
			}
		}

		for nodeID := range o.nodes {
			found := false
			for _, id := range nodeIds {
				if nodeID == id {
					found = true
					break
				}
			}
			if !found {
				delete(o.nodes, nodeID)
			}
		}

		for nodeID := range o.nodeToHost {
			activeInstances, err := o.GetInstances(ctx, nodeID)
			if err != nil {
				log.Printf("Error loading current sandboxes\n: %v", err)
			} else {
				added := instanceCache.Sync(activeInstances, nodeID)
				for _, sandbox := range added {
					o.nodes[nodeID].RamUsage += sandbox.RamMB
					o.nodes[nodeID].CPUUsage += sandbox.VCPU
				}
			}
		}
		logger.Info("Synced instances with Orchestrator")
		for _, node := range o.nodes {
			logger.Infof("Node %s: CPU: %d, RAM: %d", node.ID, node.CPUUsage, node.RamUsage)
		}

		instanceCache.SendAnalyticsEvent()
	}
}

// InitialSync loads already running instances from Orchestrator
func (o *Orchestrator) InitialSync(ctx context.Context) (instances []*instance.InstanceInfo, err error) {
	nodes, err := o.ListNodes()
	if err != nil {
		return instances, err
	}

	for _, node := range nodes {
		activeInstances, instancesErr := o.connectToNode(ctx, node)
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

func (o *Orchestrator) connectToNode(ctx context.Context, node *nomadapi.NodeListStub) ([]*instance.InstanceInfo, error) {
	n := &Node{ID: o.getIdFromNode(node)}
	o.nodes[n.ID] = n

	activeInstances, instancesErr := o.GetInstances(ctx, o.getIdFromNode(node))
	if instancesErr != nil {
		return nil, instancesErr
	}

	for _, sandbox := range activeInstances {
		n.RamUsage += sandbox.RamMB
		n.CPUUsage += sandbox.VCPU
	}
	return activeInstances, nil
}
