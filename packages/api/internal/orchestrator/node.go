package orchestrator

import (
	"fmt"

	nomadapi "github.com/hashicorp/nomad/api"

	"github.com/e2b-dev/infra/packages/api/internal/node"
	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
)

type Node struct {
	ID       string
	CPUUsage int64
	RamUsage int64
	Client   *GRPCClient
	Info     *node.NodeInfo
}

func (o *Orchestrator) listNomadNodes() ([]*node.NodeInfo, error) {
	nomadNodes, _, err := o.nomadClient.Nodes().List(&nomadapi.QueryOptions{Filter: "Status == \"ready\""})
	if err != nil {
		return nil, err
	}

	nodes := make([]*node.NodeInfo, 0, len(nomadNodes))
	for _, n := range nomadNodes {
		nodes = append(nodes, &node.NodeInfo{
			ID:                  n.ID[:consts.NodeIDLength],
			OrchestratorAddress: fmt.Sprintf("%s:%s", n.Address, consts.OrchestratorPort),
			ProxyAddress:        fmt.Sprintf("%s:%s", n.Address, consts.SessionProxyPort),
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
