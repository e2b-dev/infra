package template_manager

import (
	"github.com/launchdarkly/go-sdk-common/v3/ldvalue"

	"github.com/e2b-dev/infra/packages/shared/pkg/machineinfo"
)

func buildNodeFeatureFlagToMachineInfo(value ldvalue.Value) machineinfo.MachineInfo {
	if value.IsNull() {
		return machineinfo.MachineInfo{}
	}

	obj := value.AsValueMap().AsMap()
	return machineinfo.MachineInfo{
		CPUArchitecture: obj["cpuArchitecture"].String(),
		CPUFamily:       obj["cpuFamily"].String(),
		CPUModel:        obj["cpuModel"].String(),
		CPUModelName:    obj["cpuModelName"].String(),
	}
}
