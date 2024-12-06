package orchestrator

import (
	"fmt"

	nomadapi "github.com/hashicorp/nomad/api"

	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
)

type Node struct {
	ID            string
	CPUUsage      int64
	RamUsage      int64
	Client        *GRPCClient
	sbxInProgress int
}

type nodeInfo struct {
	ID      string
	Address string
}

func (o *Orchestrator) listNomadNodes() ([]*nodeInfo, error) {
	// TODO: Use variable for node pool name ("default")
	nomadNodes, _, err := o.nomadClient.Nodes().List(&nomadapi.QueryOptions{Filter: "Status == \"ready\" and NodePool == \"default\""})
	if err != nil {
		return nil, err
	}

	nodes := make([]*nodeInfo, 0, len(nomadNodes))
	for _, node := range nomadNodes {
		nodes = append(nodes, &nodeInfo{
			ID:      node.ID[:consts.NodeIDLength],
			Address: fmt.Sprintf("%s:%s", node.Address, consts.OrchestratorPort),
		})
	}
	return nodes, nil
}

func (o *Orchestrator) GetNode(nodeID string) (*Node, error) {
	node, ok := o.nodes[nodeID]
	if !ok {
		return nil, fmt.Errorf("node %s not found", nodeID)
	}

	return node, nil
}
