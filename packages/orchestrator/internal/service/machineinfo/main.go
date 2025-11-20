package machineinfo

import (
	"fmt"
	"runtime"

	"github.com/shirou/gopsutil/v4/cpu"
)

type MachineInfo struct {
	Family string
	Model  string
	Arch   string
}

func Detect() (MachineInfo, error) {
	info, err := cpu.Info()
	if err != nil {
		return MachineInfo{}, fmt.Errorf("failed to get CPU info: %w", err)
	}

	if len(info) > 0 {
		return MachineInfo{
			Family: info[0].Family,
			Model:  info[0].Model,
			Arch:   runtime.GOARCH,
		}, nil
	}

	return MachineInfo{}, fmt.Errorf("unable to detect CPU platform from any source")
}
