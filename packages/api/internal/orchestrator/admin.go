package orchestrator

import (
	"cmp"
	"slices"

	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/api/internal/api"
)

func (o *Orchestrator) AdminNodes() ([]*api.Node, error) {
	var result []*api.Node

	for _, n := range o.nodes.Items() {
		// Skip all nodes that are not running in local (Nomad) cluster
		if !n.IsNomadManaged() {
			continue
		}

		meta := n.Metadata()
		metrics := n.GetAPIMetric()
		machineInfo := n.MachineInfo()
		result = append(result, &api.Node{
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
			SandboxCount:         n.Metrics().SandboxCount,
			Version:              meta.Version,
			Commit:               meta.Commit,
			Metrics:              metrics,
		})
	}

	slices.SortFunc(result, func(i, j *api.Node) int {
		return cmp.Compare(i.Id, j.Id)
	})

	return result, nil
}

func (o *Orchestrator) AdminNodeDetail(clusterID uuid.UUID, nodeID string) (*api.NodeDetail, error) {
	n := o.GetNode(clusterID, nodeID)
	if n == nil {
		return nil, ErrNodeNotFound
	}

	meta := n.Metadata()
	metrics := n.GetAPIMetric()
	machineInfo := n.MachineInfo()

	node := &api.NodeDetail{
		Id:                n.ID,
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
		SandboxCount:    n.Metrics().SandboxCount,
		Version:         meta.Version,
		Commit:          meta.Commit,
		Metrics:         metrics,
	}

	return node, nil
}
