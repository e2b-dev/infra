package machineinfo

import (
	"encoding/json"

	"github.com/launchdarkly/go-sdk-common/v3/ldvalue"

	"github.com/e2b-dev/infra/packages/db/queries"
	infogrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator-info"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

type MachineInfo struct {
	CPUArchitecture string   `json:"cpu_architecture"`
	CPUFamily       string   `json:"cpu_family"`
	CPUModel        string   `json:"cpu_model"`
	CPUModelName    string   `json:"cpu_model_name"`
	CPUFlags        []string `json:"cpu_flags"`
}

func (m MachineInfo) IsCompatibleWith(other MachineInfo) bool {
	return m.CPUArchitecture == other.CPUArchitecture && m.CPUFamily == other.CPUFamily && m.CPUModel == other.CPUModel
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

func FromLDValue(value ldvalue.Value) MachineInfo {
	if value.IsNull() {
		return MachineInfo{}
	}

	// Parse as JSON
	var info MachineInfo
	_ = json.Unmarshal([]byte(value.String()), &info)

	return info
}
