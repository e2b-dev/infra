package host

import (
	"fmt"
	"time"

	"github.com/rs/zerolog"
	"github.com/shirou/gopsutil/v4/process"
)

type ProcessState string

const (
	ProcessStateRunning ProcessState = "running"
	ProcessStateExited  ProcessState = "exited"
)

type ProcessInfo struct {
	State      ProcessState
	PID        int32
	Name       string
	Cmdline    string
	CreateTime int64
}

type ProcessEventHandler func(processInfo *ProcessInfo) error

func getProcessInfo(pid int32) (*ProcessInfo, error) {
	p, err := process.NewProcess(pid)
	if err != nil {
		return nil, err
	}

	name, err := p.Name()
	if err != nil {
		name = "unknown"
	}

	cmdline, err := p.Cmdline()
	if err != nil {
		cmdline = ""
	}

	createTime, err := p.CreateTime()
	if err != nil {
		createTime = 0
	}

	return &ProcessInfo{
		PID:        pid,
		Name:       name,
		Cmdline:    cmdline,
		CreateTime: createTime,
	}, nil
}

func MonitorProcesses(logger *zerolog.Logger, interval time.Duration, processEventHandlers ...ProcessEventHandler) {
	knownProcesses := make(map[string]*ProcessInfo)

	// Get initial process list
	initialPids, err := process.Pids()
	if err != nil {
		logger.Error().Err(err).Msg("Failed to get initial process list")
	}

	for _, pid := range initialPids {
		if info, err := getProcessInfo(pid); err == nil {
			key := deriveKey(pid, info.Name)
			knownProcesses[key] = info
		} else {
			logger.Error().Err(err).Msg("Failed to get process info")
		}
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for range ticker.C {
		currentPids, err := process.Pids()
		if err != nil {
			logger.Error().Err(err).Msg("Error getting current processes")
			continue
		}

		currentProcesses := make(map[string]*ProcessInfo)

		for _, pid := range currentPids {
			if info, err := getProcessInfo(pid); err == nil {
				key := deriveKey(pid, info.Name)
				currentProcesses[key] = info

				// Check if this is a new process
				if _, exists := knownProcesses[key]; !exists {
					info.State = ProcessStateRunning
					for _, handler := range processEventHandlers {
						err := handler(info)
						if err != nil {
							logger.Error().Err(err).Msg("Error handling process start event")
						}
					}
				}
			}
		}

		// Check for exited processes
		for key, info := range knownProcesses {
			if _, exists := currentProcesses[key]; !exists {
				info.State = ProcessStateExited
				for _, handler := range processEventHandlers {
					err := handler(info)
					if err != nil {
						logger.Error().Err(err).Msg("Error handling process exit event")
					}
				}
			}
		}

		knownProcesses = currentProcesses
	}
}

func deriveKey(pid int32, name string) string {
	return fmt.Sprintf("%d:%s", pid, name)
}
