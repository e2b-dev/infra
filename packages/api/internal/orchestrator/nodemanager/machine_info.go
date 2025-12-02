package nodemanager

import (
	infogrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator-info"
	"github.com/e2b-dev/infra/packages/shared/pkg/machineinfo"
)

func (n *Node) setMachineInfo(info *infogrpc.MachineInfo) {
	n.mutex.Lock()
	defer n.mutex.Unlock()

	n.machineInfo = machineinfo.FromGRPCInfo(info)
}

func (n *Node) MachineInfo() machineinfo.MachineInfo {
	n.mutex.RLock()
	defer n.mutex.RUnlock()

	return n.machineInfo
}
