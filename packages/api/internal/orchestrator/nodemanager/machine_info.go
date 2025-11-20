package nodemanager

import infogrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator-info"

type MachineInfo struct {
	CPUFamily       string
	CPUModel        string
	CPUArchitecture string
}

func (n *Node) setMachineInfo(mi MachineInfo) {
	n.mutex.Lock()
	defer n.mutex.Unlock()

	n.machineInfo = mi
}

func (n *Node) MachineInfo() MachineInfo {
	n.mutex.RLock()
	defer n.mutex.RUnlock()

	return n.machineInfo
}

func MachineInfoFromGRPC(info *infogrpc.MachineInfo) MachineInfo {
	if info == nil {
		return MachineInfo{}
	}

	return MachineInfo{
		CPUFamily:       info.GetCpuFamily(),
		CPUModel:        info.GetCpuModel(),
		CPUArchitecture: info.GetCpuArchitecture(),
	}
}
