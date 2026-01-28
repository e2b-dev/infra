package orchestrator

import (
	"cmp"
	"context"
	"slices"

	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

func (o *Orchestrator) AdminNodes(ctx context.Context) ([]*api.Node, error) {
	apiNodes := make(map[string]*api.Node)

	for _, n := range o.nodes.Items() {
		// Skip all nodes that are not running in local (Nomad) cluster
		if !n.IsNomadManaged() {
			continue
		}

		meta := n.Metadata()
		metrics := n.GetAPIMetric()
		machineInfo := n.MachineInfo()
		apiNodes[n.ID] = &api.Node{
			NodeID:            n.NomadNodeShortID,
			Id:                n.ID,
			ServiceInstanceID: meta.ServiceInstanceID,
			ClusterID:         n.ClusterID.String(),
			MachineInfo: api.MachineInfo{
				CpuArchitecture: machineInfo.CPUArchitecture,
				CpuFamily:       machineInfo.CPUFamily,
				CpuModel:        machineInfo.CPUModel,
				CpuModelName:    machineInfo.CPUModelName,
			},
			Status:               n.Status(),
			CreateSuccesses:      n.PlacementMetrics.SuccessCount(),
			CreateFails:          n.PlacementMetrics.FailsCount(),
			SandboxStartingCount: int(n.PlacementMetrics.InProgressCount()),
			Version:              meta.Version,
			Commit:               meta.Commit,
			Metrics:              metrics,
		}
	}

	sbxs, err := o.sandboxStore.AllItems(ctx, []sandbox.State{sandbox.StateRunning})
	if err != nil {
		return nil, err
	}

	for _, sbx := range sbxs {
		n, ok := apiNodes[sbx.NodeID]
		if !ok {
			logger.L().Error(ctx, "node for sandbox wasn't found", logger.WithNodeID(sbx.NodeID), logger.WithSandboxID(sbx.SandboxID))

			continue
		}

		n.SandboxCount++
	}

	var result []*api.Node
	for _, n := range apiNodes {
		result = append(result, n)
	}

	slices.SortFunc(result, func(i, j *api.Node) int {
		return cmp.Compare(i.NodeID, j.NodeID)
	})

	return result, nil
}

func (o *Orchestrator) AdminNodeDetail(ctx context.Context, clusterID uuid.UUID, nodeIDOrNomadNodeShortID string) (*api.NodeDetail, error) {
	n := o.GetNodeByIDOrNomadShortID(clusterID, nodeIDOrNomadNodeShortID)
	if n == nil {
		return nil, ErrNodeNotFound
	}

	meta := n.Metadata()
	metrics := n.GetAPIMetric()
	machineInfo := n.MachineInfo()

	node := &api.NodeDetail{
		Id:                n.ID,
		NodeID:            n.NomadNodeShortID,
		ClusterID:         n.ClusterID.String(),
		ServiceInstanceID: meta.ServiceInstanceID,
		MachineInfo: api.MachineInfo{
			CpuArchitecture: machineInfo.CPUArchitecture,
			CpuFamily:       machineInfo.CPUFamily,
			CpuModel:        machineInfo.CPUModel,
			CpuModelName:    machineInfo.CPUModelName,
		},
		Status:          n.Status(),
		CreateSuccesses: n.PlacementMetrics.SuccessCount(),
		CreateFails:     n.PlacementMetrics.FailsCount(),
		Version:         meta.Version,
		Commit:          meta.Commit,
		Metrics:         metrics,
	}

	sbxs, err := o.sandboxStore.AllItems(ctx, []sandbox.State{sandbox.StateRunning})
	if err != nil {
		return nil, err
	}

	for _, sbx := range sbxs {
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
				EndAt:      sbx.EndTime,
				Metadata:   metadata,
				SandboxID:  sbx.SandboxID,
				StartedAt:  sbx.StartTime,
				TemplateID: sbx.TemplateID,
			})
		}
	}

	return node, nil
}
