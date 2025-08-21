package orchestrator

import (
	"cmp"
	"slices"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/orchestrator/nodemanager"
	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

func (o *Orchestrator) AdminNodes() []*api.Node {
	apiNodes := make(map[string]*api.Node)

	for _, n := range o.nodes.Items() {
		// Skip all nodes that are not running in local (Nomad) cluster
		if n.NomadNodeShortID == nodemanager.UnknownNomadNodeShortID {
			continue
		}

		meta := n.Metadata()
		metrics := n.GetAPIMetric()
		apiNodes[n.ID] = &api.Node{
			NodeID:               n.NomadNodeShortID,
			ClusterID:            n.ClusterID.String(),
			Status:               n.Status(),
			CreateSuccesses:      n.PlacementMetrics.SuccessCount(),
			CreateFails:          n.PlacementMetrics.FailsCount(),
			SandboxStartingCount: int(n.PlacementMetrics.InProgressCount()),
			Version:              meta.Version,
			Commit:               meta.Commit,
			Metrics:              metrics,
		}
	}

	for _, sbx := range o.instanceCache.Items() {
		n, ok := apiNodes[sbx.NodeID]
		if !ok {
			zap.L().Error("node for sandbox wasn't found", logger.WithNodeID(sbx.NodeID), logger.WithSandboxID(sbx.SandboxID))
			continue
		}

		n.SandboxCount += 1
	}

	var result []*api.Node
	for _, n := range apiNodes {
		result = append(result, n)
	}

	slices.SortFunc(result, func(i, j *api.Node) int {
		return cmp.Compare(i.NodeID, j.NodeID)
	})

	return result
}

func (o *Orchestrator) AdminNodeDetail(nomadNodeShortID string) (*api.NodeDetail, error) {
	n := o.GetNodeByNomadShortID(nomadNodeShortID)
	if n == nil {
		return nil, ErrNodeNotFound
	}

	meta := n.Metadata()
	metrics := n.GetAPIMetric()

	node := &api.NodeDetail{
		NodeID:    n.NomadNodeShortID,
		ClusterID: n.ClusterID.String(),

		Status:          n.Status(),
		CreateSuccesses: n.PlacementMetrics.SuccessCount(),
		CreateFails:     n.PlacementMetrics.FailsCount(),
		Version:         meta.Version,
		Commit:          meta.Commit,
		Metrics:         metrics,
	}

	for _, sbx := range o.instanceCache.Items() {
		if sbx.NodeID == n.ID && sbx.ClusterID == n.ClusterID {
			var metadata *api.SandboxMetadata
			if sbx.Metadata != nil {
				meta := api.SandboxMetadata(sbx.Metadata)
				metadata = &meta
			}

			node.Sandboxes = append(node.Sandboxes, api.ListedSandbox{
				Alias:      sbx.Alias,
				ClientID:   consts.ClientID,
				CpuCount:   api.CPUCount(sbx.VCpu),
				MemoryMB:   api.MemoryMB(sbx.RamMB),
				DiskSizeMB: api.DiskSizeMB(sbx.TotalDiskSizeMB),
				EndAt:      sbx.GetEndTime(),
				Metadata:   metadata,
				SandboxID:  sbx.SandboxID,
				StartedAt:  sbx.StartTime,
				TemplateID: sbx.TemplateID,
			})
		}
	}

	return node, nil
}
