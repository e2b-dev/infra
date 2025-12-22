package clusters

import (
	"slices"
	"sync"

	infogrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator-info"
	"github.com/e2b-dev/infra/packages/shared/pkg/machineinfo"
)

type Instance struct {
	NodeID     string
	InstanceID string

	ServiceVersion       string
	ServiceVersionCommit string

	roles       []infogrpc.ServiceInfoRole
	machineInfo machineinfo.MachineInfo

	status infogrpc.ServiceInfoStatus
	mutex  sync.RWMutex
}

func (n *Instance) GetStatus() infogrpc.ServiceInfoStatus {
	n.mutex.RLock()
	defer n.mutex.RUnlock()

	return n.status
}

func (n *Instance) GetMachineInfo() machineinfo.MachineInfo {
	n.mutex.RLock()
	defer n.mutex.RUnlock()

	return n.machineInfo
}

func (n *Instance) hasRole(r infogrpc.ServiceInfoRole) bool {
	n.mutex.RLock()
	defer n.mutex.RUnlock()

	return slices.Contains(n.roles, r)
}

func (n *Instance) IsBuilder() bool {
	return n.hasRole(infogrpc.ServiceInfoRole_TemplateBuilder)
}

func (n *Instance) IsOrchestrator() bool {
	return n.hasRole(infogrpc.ServiceInfoRole_Orchestrator)
}
