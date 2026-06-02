package machineinfo

import (
	"context"
	"encoding/json"
	"strconv"

	"github.com/launchdarkly/go-sdk-common/v3/ldvalue"
	"go.uber.org/zap"

	infogrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator-info"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

const (
	archAMD64 = "amd64"

	// intelCPUFamily is CPU family 6, used by modern Intel CPUs. (e.g. N2, C3, C4 nodes in GCP)
	intelCPUFamily = "6"

	// minForwardCompatibleModel is the minimum CPU model (Ice Lake) from which
	// newer Intel CPUs are treated as forward-compatible.
	minForwardCompatibleModel = 106
)

type MachineInfo struct {
	CPUArchitecture string   `json:"cpu_architecture"`
	CPUFamily       string   `json:"cpu_family"`
	CPUModel        string   `json:"cpu_model"`
	CPUModelName    string   `json:"cpu_model_name"`
	CPUFlags        []string `json:"cpu_flags"`
}

// IsCompatibleWith reports whether a build (the receiver m) can run on a node
// (the argument other). The receiver must be the build's machine info and the
// argument must be the node's machine info; the relationship is not symmetric.
func (m MachineInfo) IsCompatibleWith(other MachineInfo) bool {
	// Architecture and family must always match.
	if m.CPUArchitecture != other.CPUArchitecture || m.CPUFamily != other.CPUFamily {
		return false
	}

	// For amd64 builds on CPU family 6 with model >= minForwardCompatibleModel
	// (Ice Lake and newer Intel), a node is compatible as long as its CPU model
	// is >= the build's CPU model, i.e. newer Intel CPUs are forward-compatible.
	if m.CPUArchitecture == archAMD64 {
		buildModel, buildErr := strconv.Atoi(m.CPUModel)
		nodeModel, nodeErr := strconv.Atoi(other.CPUModel)
		if buildErr == nil && nodeErr == nil && m.CPUFamily == intelCPUFamily && buildModel >= minForwardCompatibleModel {
			return nodeModel >= buildModel
		}
	}

	// Default: require an exact CPU model match.
	return m.IsExactMatch(other)
}

func (m MachineInfo) IsExactMatch(other MachineInfo) bool {
	return m.CPUArchitecture == other.CPUArchitecture &&
		m.CPUFamily == other.CPUFamily &&
		m.CPUModel == other.CPUModel
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
