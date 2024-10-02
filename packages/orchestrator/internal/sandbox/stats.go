package sandbox

import (
	"fmt"

	"github.com/shirou/gopsutil/v4/process"
)

type ProcStats struct {
	PID        int32   `json:"pid"`
	Name       string  `json:"name"`
	CPUPercent float64 `json:"cpu_pct"`
	VMS        uint64  `json:"vms"`
	RSS        uint64  `json:"rss"`
}

func (p *ProcStats) String() string {
	return fmt.Sprintf(
		"[PID: %d] [Name: %s] CPU (%%): %.2f | VMS (MB): %.2f | RSS (MB): %.2f",
		p.PID, p.Name, p.CPUPercent, float64(p.VMS)/1024/1024, float64(p.RSS)/1024/1024,
	)
}

func getProcStats(pid int32, procStats *[]ProcStats) error {
	proc, err := process.NewProcess(pid)
	if err != nil {
		return fmt.Errorf("failed to create new a new Process instance: %w", err)
	}

	procChildren, _ := proc.Children()
	for _, procChild := range procChildren {
		childName, err := procChild.Name()
		if err != nil {
			return fmt.Errorf("failed to get child process name: %w", err)
		}

		fmt.Println("child PID:", procChild.Pid, "name:", childName)

		err = getProcStats(int32(procChild.Pid), procStats)
		if err != nil {
			return fmt.Errorf("failed to get child process stats: %w", err)
		}
	}

	procName, err := proc.Name()
	if err != nil {
		return fmt.Errorf("failed to get process name: %w", err)
	}

	cpuPercent, err := proc.CPUPercent()
	if err != nil {
		return fmt.Errorf("failed to get CPU percent: %w", err)
	}

	memory, err := proc.MemoryInfo()
	if err != nil {
		return fmt.Errorf("failed to get memory info: %w", err)
	}

	procStat := ProcStats{
		PID:  proc.Pid,
		Name: procName,

		CPUPercent: cpuPercent,
		// RSS (Resident Set Size) is the closest available metric.
		RSS: memory.RSS,
		// VMS (Virtual Memory Size) is the total size of the process's virtual address space.
		VMS: memory.VMS,
	}

	*procStats = append(*procStats, procStat)

	return nil
}
