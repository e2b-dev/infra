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

func FromLDValue(ctx context.Context, value ldvalue.Value) MachineInfo {
	var info MachineInfo
	err := json.Unmarshal([]byte(value.JSONString()), &info)
	if err != nil {
		logger.L().Error(ctx, "failed to unmarshal machine info", zap.Error(err))

		return MachineInfo{}
	}

	return info
}
