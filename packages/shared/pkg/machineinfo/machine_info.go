package machineinfo

import (
	"context"
	"encoding/json"

	"github.com/launchdarkly/go-sdk-common/v3/ldvalue"
	"go.uber.org/zap"

	infogrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator-info"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

type MachineInfo struct {
	CPUArchitecture string   `json:"cpu_architecture"`
	CPUFamily       string   `json:"cpu_family"`
	CPUModel        string   `json:"cpu_model"`
	CPUModelName    string   `json:"cpu_model_name"`
	CPUFlags        []string `json:"cpu_flags"`
}

// IsCompatibleWith reports whether a guest built on m's CPU can run on a node
// with the other CPU. The node must support every CPU flag the guest was built
// with, which allows restoring across generations (e.g. n2 -> n4) while rejecting
// a node with a smaller instruction set. When the guest has no recorded flags
// (older builds), it falls back to the stricter family/model match.
func (m MachineInfo) IsCompatibleWith(other MachineInfo) bool {
	if m.CPUArchitecture != other.CPUArchitecture {
		return false
	}

	if len(m.CPUFlags) == 0 {
		return m.CPUFamily == other.CPUFamily && m.CPUModel == other.CPUModel
	}

	nodeFlags := make(map[string]struct{}, len(other.CPUFlags))
	for _, f := range other.CPUFlags {
		nodeFlags[f] = struct{}{}
	}

	for _, f := range m.CPUFlags {
		if _, ok := nodeFlags[f]; !ok {
			return false
		}
	}

	return true
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

func FromLDValue(ctx context.Context, value ldvalue.Value) MachineInfo {
	var info MachineInfo
	err := json.Unmarshal([]byte(value.JSONString()), &info)
	if err != nil {
		logger.L().Error(ctx, "failed to unmarshal machine info", zap.Error(err))

		return MachineInfo{}
	}

	return info
}
