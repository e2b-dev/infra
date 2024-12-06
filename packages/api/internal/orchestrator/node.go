package orchestrator

import (
	"fmt"
	"time"

	nomadapi "github.com/hashicorp/nomad/api"
	"github.com/jellydator/ttlcache/v3"

	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

type sbxInProgress struct {
	MiBMemory int64
	CPUs      int64
}

type Node struct {
	ID       string
	CPUUsage int64
	RamUsage int64
	Client   *GRPCClient

	sbxsInProgress map[string]*sbxInProgress
	buildCache     *ttlcache.Cache[string, interface{}]
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

func (n *Node) SyncBuilds(builds []*orchestrator.CachedBuildInfo) {
	for _, build := range builds {
		n.buildCache.Set(build.BuildId, struct{}{}, build.ExpirationTime.AsTime().Sub(time.Now()))
	}
}

func (t *Node) InsertBuild(buildID string) {
	exists := t.buildCache.Has(buildID)
	if exists {
		return
	}

	// Set the build in the cache for 2 minutes, it should get updated with the correct time from the orchestrator during sync
	t.buildCache.Set(buildID, struct{}{}, 2*time.Minute)
	return
}
