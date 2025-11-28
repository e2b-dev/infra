package machineinfo

import infogrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator-info"

type MachineInfo struct {
	CPUArchitecture string
	CPUFamily       string
	CPUModel        string
	CPUModelName    string
	CPUFlags        []string
}

func FromGRPCInfo(info *infogrpc.MachineInfo) MachineInfo {
	if info == nil {
		return MachineInfo{}
	}

	return MachineInfo{
		CPUArchitecture: info.GetCpuArchitecture(),
		CPUFamily:       info.GetCpuFamily(),
		CPUModel:        info.GetCpuModel(),
		CPUModelName:    info.GetCpuModelName(),
		CPUFlags:        info.GetCpuFlags(),
	}
}
