package sandbox

import (
	"fmt"

	"github.com/shirou/gopsutil/v4/process"
)

type ProcStats struct {
	PID        int32                   `json:"pid"`
	Name       string                  `json:"name"`
	CPUPercent float64                 `json:"cpu_pct"`
	MemoryInfo *process.MemoryInfoStat `json:"memory_info"`
}

func (p *ProcStats) String() string {
	return fmt.Sprintf(
		"[PID: %d] [Name: %s] CPU (%%): %.2f | VMS (MB): %.2f | RSS (MB): %.2f | Swap (MB): %.2f | Stack: %.2f",
		p.PID, p.Name, p.CPUPercent, float64(p.MemoryInfo.VMS)/1024/1024, float64(p.MemoryInfo.RSS)/1024/1024, float64(p.MemoryInfo.Swap)/1024/1024, float64(p.MemoryInfo.Stack/1024/1024),
	)
}

func getProcStats(pid int32, procStats *[]ProcStats) error {
	proc, err := process.NewProcess(pid)
	if err != nil {
		return fmt.Errorf("failed to create new a new Process instance: %w", err)
	}

	procChildren, _ := proc.Children()
	for _, procChild := range procChildren {
		err = getProcStats(int32(procChild.Pid), procStats)
		if err != nil {
			return fmt.Errorf("failed to get child process stats: %w", err)
		}
	}

	procName, err := proc.Name()
	if err != nil {
		return fmt.Errorf("failed to get process name: %w", err)
	}

	if procName == "unshare" { // unshare is not relevant to us
		return nil
	}

	cpuPercent, err := proc.CPUPercent()
	if err != nil {
		return fmt.Errorf("failed to get CPU percent: %w", err)
	}

	memoryInfo, err := proc.MemoryInfo()
	if err != nil {
		return fmt.Errorf("failed to get memory info: %w", err)
	}

	procStat := ProcStats{
		PID:  proc.Pid,
		Name: procName,

		CPUPercent: cpuPercent,
		MemoryInfo: memoryInfo,
	}

	*procStats = append(*procStats, procStat)

	return nil
}
