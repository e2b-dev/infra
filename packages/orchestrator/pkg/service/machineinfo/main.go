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
		family := info[0].Family
		model := info[0].Model

		// On ARM64, gopsutil doesn't populate Family/Model from /proc/cpuinfo.
		// Provide fallback values so callers don't get an error.
		// NOTE: Using a generic "arm64" family treats all ARM64 CPUs as compatible.
		// This works for same-host snapshot restore but cross-host restore between
		// different ARM CPU implementations (e.g. Graviton2 vs Graviton3) may fail.
		// For finer granularity, consider using MIDR_EL1 register values.
		if runtime.GOARCH == "arm64" {
			if family == "" {
				family = "arm64"
			}
			if model == "" {
				model = "0"
			}
		}

		if family == "" || model == "" {
			return MachineInfo{}, fmt.Errorf("unable to detect CPU platform from CPU info: %+v", info[0])
		}

		return MachineInfo{
			Family:    family,
			Model:     model,
			ModelName: info[0].ModelName,
			Flags:     info[0].Flags,
			Arch:      runtime.GOARCH,
		}, nil
	}

	return MachineInfo{}, fmt.Errorf("unable to detect CPU platform from any source")
}
