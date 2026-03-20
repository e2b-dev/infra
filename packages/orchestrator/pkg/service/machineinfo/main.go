package machineinfo

import (
	"fmt"
	"runtime"

	"github.com/shirou/gopsutil/v4/cpu"
)

type MachineInfo struct {
	Family    string
	Model     string
	ModelName string
	Flags     []string
	Arch      string
}

func Detect() (MachineInfo, error) {
	info, err := cpu.Info()
	if err != nil {
		return MachineInfo{}, fmt.Errorf("failed to get CPU info: %w", err)
	}

	if len(info) > 0 {
		if info[0].Family == "" || info[0].Model == "" {
			return MachineInfo{}, fmt.Errorf("unable to detect CPU platform from CPU info: %+v", info[0])
		}

		return MachineInfo{
			Family:    info[0].Family,
			Model:     info[0].Model,
			ModelName: info[0].ModelName,
			Flags:     info[0].Flags,
			Arch:      runtime.GOARCH,
		}, nil
	}

	return MachineInfo{}, fmt.Errorf("unable to detect CPU platform from any source")
}
