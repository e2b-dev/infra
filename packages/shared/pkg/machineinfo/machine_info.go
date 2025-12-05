package machineinfo

import (
	"github.com/e2b-dev/infra/packages/db/queries"
	infogrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator-info"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

type MachineInfo struct {
	CPUArchitecture string
	CPUFamily       string
	CPUModel        string
	CPUModelName    string
	CPUFlags        []string
}

func (m MachineInfo) IsCompatibleWith(other MachineInfo) bool {
	return m.CPUArchitecture == other.CPUArchitecture && m.CPUFamily == other.CPUFamily
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

func FromDB(build queries.EnvBuild) MachineInfo {
	return MachineInfo{
		CPUArchitecture: utils.FromPtr(build.CpuArchitecture),
		CPUFamily:       utils.FromPtr(build.CpuFamily),
		CPUModel:        utils.FromPtr(build.CpuModel),
		CPUModelName:    utils.FromPtr(build.CpuModelName),
		CPUFlags:        build.CpuFlags,
	}
}
