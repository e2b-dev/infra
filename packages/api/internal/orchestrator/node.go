package orchestrator

import (
	"context"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	nomadapi "github.com/hashicorp/nomad/api"
	"github.com/jellydator/ttlcache/v3"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/node"
	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
)

type sbxInProgress struct {
	MiBMemory int64
	CPUs      int64
}

type Node struct {
	CPUUsage atomic.Int64
	RamUsage atomic.Int64
	Client   *GRPCClient

	Info *node.NodeInfo

	status   api.NodeStatus
	statusMu sync.RWMutex

	sbxsInProgress *smap.Map[*sbxInProgress]

	buildCache *ttlcache.Cache[string, interface{}]

	createFails atomic.Uint64
}

func (n *Node) Status() api.NodeStatus {
	n.statusMu.RLock()
	defer n.statusMu.RUnlock()

	return n.status
}

func (n *Node) SetStatus(status api.NodeStatus) {
	n.statusMu.Lock()
	defer n.statusMu.Unlock()

	n.status = status
}

func (o *Orchestrator) listNomadNodes(ctx context.Context) ([]*node.NodeInfo, error) {
	_, listSpan := o.tracer.Start(ctx, "list-nomad-nodes")
	defer listSpan.End()

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
			IPAddress:           n.Address,
		})
	}

	return nodes, nil
}

func (o *Orchestrator) GetNode(nodeID string) *Node {
	n, _ := o.nodes.Get(nodeID)
	return n
}

func (o *Orchestrator) GetNodes() []*api.Node {
	nodes := make(map[string]*api.Node)
	for key, n := range o.nodes.Items() {
		nodes[key] = &api.Node{
			NodeID:      key,
			Status:      n.Status(),
			CreateFails: n.createFails.Load(),
		}
	}

	for _, sbx := range o.instanceCache.Items() {
		n, ok := nodes[sbx.Instance.ClientID]
		if !ok {
			fmt.Fprintf(os.Stderr, "node [%s] for sandbox [%s] wasn't found \n", sbx.Instance.ClientID, sbx.Instance.SandboxID)
			continue
		}

		n.AllocatedCPU += int32(sbx.VCpu)
		n.AllocatedMemoryMiB += int32(sbx.RamMB)
		n.SandboxCount += 1
	}

	var result []*api.Node
	for _, n := range nodes {
		result = append(result, n)
	}

	return result
}

func (o *Orchestrator) GetNodeDetail(nodeID string) *api.NodeDetail {
	var node *api.NodeDetail

	for key, n := range o.nodes.Items() {
		if key == nodeID {
			builds := n.buildCache.Keys()
			node = &api.NodeDetail{
				NodeID:       key,
				Status:       n.Status(),
				CachedBuilds: builds,
				CreateFails:  n.createFails.Load(),
			}
		}
	}

	if node == nil {
		return nil
	}

	for _, sbx := range o.instanceCache.Items() {
		if sbx.Instance.ClientID == nodeID {
			var metadata *api.SandboxMetadata
			if sbx.Metadata != nil {
				meta := api.SandboxMetadata(sbx.Metadata)
				metadata = &meta
			}
			node.Sandboxes = append(node.Sandboxes, api.RunningSandbox{
				Alias:      sbx.Instance.Alias,
				ClientID:   nodeID,
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
