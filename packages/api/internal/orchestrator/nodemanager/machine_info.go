package nodemanager

import (
	infogrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator-info"
)

type MachineInfo struct {
	CPUArchitecture string
	CPUFamily       string
	CPUModel        string
	CPUModelName    string
	CPUFlags        []string
}

func (n *Node) setMachineInfo(info *infogrpc.MachineInfo) {
	n.mutex.Lock()
	defer n.mutex.Unlock()

	if info == nil {
		n.machineInfo = MachineInfo{}

		return
	}

	n.machineInfo = MachineInfo{
		CPUArchitecture: info.GetCpuArchitecture(),
		CPUFamily:       info.GetCpuFamily(),
		CPUModel:        info.GetCpuModel(),
		CPUModelName:    info.GetCpuModelName(),
		CPUFlags:        info.GetCpuFlags(),
	}
}

func (n *Node) MachineInfo() MachineInfo {
	n.mutex.RLock()
	defer n.mutex.RUnlock()

	return n.machineInfo
}
