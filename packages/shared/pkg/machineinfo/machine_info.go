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

// compatibleModelGroups lists CPU models (same architecture and family) that are
// instruction-set compatible for running a guest across machine generations.
// Per-node CPU flag sets are not reliably reported across generations, so the
// cross-model compatibility is hardcoded here.
var compatibleModelGroups = [][]string{
	{"85", "207"}, // Intel: n2 (Cascade Lake) <-> n4 (Emerald Rapids)
}

// IsCompatibleWith reports whether a guest built on m's CPU can run on a node
// with the other CPU. The same architecture+family+model is always compatible;
// cross-model compatibility is restricted to the hardcoded compatibleModelGroups
// (e.g. an n2 build restoring on an n4 node).
func (m MachineInfo) IsCompatibleWith(other MachineInfo) bool {
	if m.CPUArchitecture != other.CPUArchitecture || m.CPUFamily != other.CPUFamily {
		return false
	}

	if m.CPUModel == other.CPUModel {
		return true
	}

	for _, group := range compatibleModelGroups {
		var hasGuest, hasNode bool
		for _, model := range group {
			if model == m.CPUModel {
				hasGuest = true
			}
			if model == other.CPUModel {
				hasNode = true
			}
		}
		if hasGuest && hasNode {
			return true
		}
	}

	return false
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
