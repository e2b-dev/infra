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

// IsCompatibleWith reports whether a guest built or snapshotted on m's CPU can
// run on a node with the other CPU. Compatibility is decided by the instruction
// set, not the CPU family/model: the node (other) must support every CPU flag the
// guest was built with, otherwise an instruction the guest uses (e.g. AVX-512)
// may be missing and fault at runtime.
//
// CPUFamily/CPUModel are deliberately not compared. The raw CPU family number is
// the same ("6") for every modern Intel generation, so it can't distinguish n1
// from n2 from n4, and exact-model matching is too strict. The flag superset
// captures real compatibility: it allows restoring an older-generation build on a
// newer node (e.g. n2 -> n4, whose flags are a superset) while rejecting a move
// to a node with a smaller instruction set (e.g. n2 -> an older n1).
func (m MachineInfo) IsCompatibleWith(other MachineInfo) bool {
	if m.CPUArchitecture != other.CPUArchitecture {
		return false
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
