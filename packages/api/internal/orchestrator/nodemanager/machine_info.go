package nodemanager

import (
	infogrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator-info"
)

type MachineInfo struct {
	CPUFamily       string
	CPUModel        string
	CPUModelName    string
	CPUArchitecture string
}

func (n *Node) setMachineInfo(info *infogrpc.MachineInfo) {
	n.mutex.Lock()
	defer n.mutex.Unlock()

	if info == nil {
		n.machineInfo = MachineInfo{}
	}

	n.machineInfo = MachineInfo{
		CPUFamily:       info.GetCpuFamily(),
		CPUModel:        info.GetCpuModel(),
		CPUModelName:    info.GetCpuModelName(),
		CPUArchitecture: info.GetCpuArchitecture(),
	}
}

func (n *Node) MachineInfo() MachineInfo {
	n.mutex.RLock()
	defer n.mutex.RUnlock()

	return n.machineInfo
}
