package orchestrator

import (
	"fmt"
	"time"

	nomadapi "github.com/hashicorp/nomad/api"
	"github.com/jellydator/ttlcache/v3"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/node"
	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

type sbxInProgress struct {
	MiBMemory int64
	CPUs      int64
}

type Node struct {
	CPUUsage int64
	RamUsage int64
	Client   *GRPCClient
	Status   api.NodeStatus
	Info     *node.NodeInfo

	sbxsInProgress map[string]*sbxInProgress
	buildCache     *ttlcache.Cache[string, interface{}]
}

type nodeInfo struct {
	ID      string
	Address string
}

func (o *Orchestrator) listNomadNodes() ([]*node.NodeInfo, error) {
	// TODO: Use variable for node pool name ("default")
	nomadNodes, _, err := o.nomadClient.Nodes().List(&nomadapi.QueryOptions{Filter: "Status == \"ready\" and NodePool == \"default\""})
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

func (o *Orchestrator) GetNode(nodeID string) *Node {
	node, _ := o.nodes[nodeID]
	return node
}

func (o *Orchestrator) GetNodes() []*api.Node {
	nodes := make(map[string]*api.Node)
	for key, node := range o.nodes {
		nodes[key] = &api.Node{NodeID: key, Status: node.Status}
	}

	fmt.Println("o.instanceCache.Items()", o.instanceCache.Items())
	for _, sbx := range o.instanceCache.Items() {
		nodes[sbx.Instance.ClientID].AllocatedCPU += int32(sbx.VCpu)
		nodes[sbx.Instance.ClientID].AllocatedMemoryMiB += int32(sbx.RamMB)
		nodes[sbx.Instance.ClientID].SandboxCount += 1
	}

	var result []*api.Node
	for _, node := range nodes {
		result = append(result, node)
	}

	return result
}

func (o *Orchestrator) GetNodeDetail(nodeId string) *api.NodeDetail {
	var node *api.NodeDetail
	for key, n := range o.nodes {
		if key == nodeId {
			builds := n.buildCache.Keys()
			node = &api.NodeDetail{NodeID: key, Status: n.Status, CachedBuilds: builds}
		}
	}

	if node == nil {
		return nil
	}

	for _, sbx := range o.instanceCache.Items() {
		if sbx.Instance.ClientID == nodeId {
			var metadata *api.SandboxMetadata
			if sbx.Metadata != nil {
				meta := api.SandboxMetadata(sbx.Metadata)
				metadata = &meta
			}
			node.Sandboxes = append(node.Sandboxes, api.RunningSandbox{
				Alias:      sbx.Instance.Alias,
				ClientID:   nodeId,
				CpuCount:   api.CPUCount(sbx.VCpu),
				MemoryMB:   api.MemoryMB(sbx.RamMB),
				EndAt:      sbx.EndTime,
				Metadata:   metadata,
				SandboxID:  sbx.Instance.SandboxID,
				StartedAt:  sbx.StartTime,
				TemplateID: sbx.Instance.TemplateID,
			})
		}
	}

	return node
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
