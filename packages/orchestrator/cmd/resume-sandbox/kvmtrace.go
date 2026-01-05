package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// KVMTracer captures KVM events using ftrace.
type KVMTracer struct {
	tracingDir    string
	events        []string
	startTime     time.Time
	startWallTime int64 // Wall clock nanoseconds when Start() was called
}

// NewKVMTracer creates a new KVM tracer.
func NewKVMTracer() (*KVMTracer, error) {
	// Find the tracing directory
	tracingDir := "/sys/kernel/debug/tracing"
	if _, err := os.Stat(tracingDir); os.IsNotExist(err) {
		tracingDir = "/sys/kernel/tracing"
		if _, err := os.Stat(tracingDir); os.IsNotExist(err) {
			return nil, fmt.Errorf("tracing directory not found (debugfs not mounted?)")
		}
	}

	return &KVMTracer{
		tracingDir: tracingDir,
		events: []string{
			"kvm:kvm_entry",
			"kvm:kvm_exit",
			"kvm:kvm_mmio",
			"kvm:kvm_userspace_exit",
			"kvm:kvm_pio",
			"kvm:kvm_vcpu_wakeup", // vCPU wakeup after HLT
		},
	}, nil
}

// Start begins tracing KVM events.
func (t *KVMTracer) Start() error {
	// Disable tracing first
	if err := os.WriteFile(filepath.Join(t.tracingDir, "tracing_on"), []byte("0"), 0o644); err != nil {
		return fmt.Errorf("failed to disable tracing: %w", err)
	}

	// Clear the trace buffer
	if err := os.WriteFile(filepath.Join(t.tracingDir, "trace"), []byte(""), 0o644); err != nil {
		return fmt.Errorf("failed to clear trace: %w", err)
	}

	// Set up tracer
	if err := os.WriteFile(filepath.Join(t.tracingDir, "current_tracer"), []byte("nop"), 0o644); err != nil {
		return fmt.Errorf("failed to set tracer: %w", err)
	}

	// Enable KVM events
	for _, event := range t.events {
		eventPath := filepath.Join(t.tracingDir, "events", strings.Replace(event, ":", "/", 1), "enable")
		if _, err := os.Stat(eventPath); err == nil {
			if err := os.WriteFile(eventPath, []byte("1"), 0o644); err != nil {
				// Non-fatal: some events might not be available
				continue
			}
		}
	}

	t.startTime = time.Now()
	t.startWallTime = t.startTime.UnixNano()

	// Enable tracing
	if err := os.WriteFile(filepath.Join(t.tracingDir, "tracing_on"), []byte("1"), 0o644); err != nil {
		return fmt.Errorf("failed to enable tracing: %w", err)
	}

	return nil
}

// StartWallTime returns the wall clock nanoseconds when tracing started.
func (t *KVMTracer) StartWallTime() int64 {
	return t.startWallTime
}

// Stop stops tracing and returns the captured events.
func (t *KVMTracer) Stop() (string, error) {
	// Disable tracing
	if err := os.WriteFile(filepath.Join(t.tracingDir, "tracing_on"), []byte("0"), 0o644); err != nil {
		return "", fmt.Errorf("failed to disable tracing: %w", err)
	}

	// Disable events
	for _, event := range t.events {
		eventPath := filepath.Join(t.tracingDir, "events", strings.Replace(event, ":", "/", 1), "enable")
		if _, err := os.Stat(eventPath); err == nil {
			os.WriteFile(eventPath, []byte("0"), 0o644)
		}
	}

	// Read trace buffer
	trace, err := os.ReadFile(filepath.Join(t.tracingDir, "trace"))
	if err != nil {
		return "", fmt.Errorf("failed to read trace: %w", err)
	}

	return string(trace), nil
}

// KVMStats holds aggregated KVM statistics.
type KVMStats struct {
	TotalEntries int
	TotalExits   int
	ExitReasons  map[string]int
	Duration     time.Duration
	Events       []KVMEvent // Individual parsed events
}

// KVMEvent represents a single KVM trace event.
type KVMEvent struct {
	TimestampNs int64  // Nanoseconds since trace start
	TimestampUs float64 // Microseconds for display
	EventType   string // "entry", "exit", "hlt", "wakeup", "mmio", "pio", "sched"
	VCPU        int    // vCPU number
	Reason      string // Exit reason (for exit events)
	Details     string // Additional details
}

// ParseKVMTrace parses the raw ftrace output and returns aggregated stats.
func ParseKVMTrace(trace string, pid int) *KVMStats {
	stats := &KVMStats{
		ExitReasons: make(map[string]int),
		Events:      make([]KVMEvent, 0),
	}

	pidStr := strconv.Itoa(pid)
	var baseTs float64 = -1

	for _, line := range strings.Split(trace, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Filter by PID if specified
		if pid > 0 && !strings.Contains(line, pidStr) {
			continue
		}

		// Parse timestamp from ftrace line format:
		// process-pid [cpu] timestamp: event_name: details
		// e.g.: "fc_vcpu 0-12345 [001] 123456.789012: kvm_exit: reason HLT..."
		ts, event := parseKVMTraceLine(line)
		if event.EventType != "" {
			if baseTs < 0 {
				baseTs = ts
			}
			// Convert to nanoseconds relative to start
			relativeUs := (ts - baseTs) * 1e6 // seconds to microseconds
			event.TimestampUs = relativeUs
			event.TimestampNs = int64(relativeUs * 1000) // us to ns
			stats.Events = append(stats.Events, event)
		}

		if strings.Contains(line, "kvm_entry") {
			stats.TotalEntries++
		} else if strings.Contains(line, "kvm_exit") {
			stats.TotalExits++

			// Extract exit reason
			if idx := strings.Index(line, "reason"); idx >= 0 {
				reason := line[idx:]
				if endIdx := strings.Index(reason, " "); endIdx > 0 {
					reason = reason[:endIdx]
				}
				stats.ExitReasons[reason]++
			}
		}
	}

	return stats
}

// parseKVMTraceLine extracts timestamp and event info from an ftrace line.
func parseKVMTraceLine(line string) (float64, KVMEvent) {
	event := KVMEvent{}

	// Find timestamp - look for pattern like "123456.789012:"
	// ftrace format: "          <idle>-0     [001]   123.456789: kvm_exit: ..."
	parts := strings.Fields(line)
	if len(parts) < 4 {
		return 0, event
	}

	var ts float64
	for i, part := range parts {
		// Timestamp ends with ":"
		if strings.HasSuffix(part, ":") && strings.Contains(part, ".") {
			tsStr := strings.TrimSuffix(part, ":")
			var err error
			ts, err = strconv.ParseFloat(tsStr, 64)
			if err != nil {
				continue
			}

			// Next part should be the event name
			if i+1 < len(parts) {
				eventName := strings.TrimSuffix(parts[i+1], ":")

				switch {
				case eventName == "kvm_exit":
					event.EventType = "exit"
					// Parse reason
					for j := i + 2; j < len(parts); j++ {
						if strings.HasPrefix(parts[j], "reason") {
							event.Reason = strings.TrimPrefix(parts[j], "reason")
							event.Reason = strings.Trim(event.Reason, "= ")
							// Check if reason is HLT
							if j+1 < len(parts) && (parts[j+1] == "HLT" || strings.ToLower(parts[j+1]) == "hlt") {
								event.EventType = "hlt"
								event.Reason = "HLT"
							}
						}
						if event.Reason == "" && (parts[j] == "HLT" || strings.ToLower(parts[j]) == "hlt") {
							event.EventType = "hlt"
							event.Reason = "HLT"
						}
					}
					// Collect details
					if i+2 < len(parts) {
						event.Details = strings.Join(parts[i+2:], " ")
					}

				case eventName == "kvm_entry":
					event.EventType = "entry"
					// Parse vcpu
					for j := i + 2; j < len(parts); j++ {
						if strings.HasPrefix(parts[j], "vcpu") {
							vcpuStr := strings.TrimPrefix(parts[j], "vcpu")
							vcpuStr = strings.Trim(vcpuStr, "= ")
							event.VCPU, _ = strconv.Atoi(vcpuStr)
						}
					}

				case eventName == "kvm_vcpu_wakeup":
					event.EventType = "wakeup"
					if i+2 < len(parts) {
						event.Details = strings.Join(parts[i+2:], " ")
					}

				case eventName == "kvm_mmio":
					event.EventType = "mmio"
					if i+2 < len(parts) {
						event.Details = strings.Join(parts[i+2:], " ")
					}

				case eventName == "kvm_pio":
					event.EventType = "pio"
					if i+2 < len(parts) {
						event.Details = strings.Join(parts[i+2:], " ")
					}

				case eventName == "sched_switch":
					event.EventType = "sched"
					if i+2 < len(parts) {
						event.Details = strings.Join(parts[i+2:], " ")
					}

				case eventName == "cpu_idle":
					event.EventType = "cpu_idle"
					if i+2 < len(parts) {
						event.Details = strings.Join(parts[i+2:], " ")
					}
				}
			}
			break
		}
	}

	return ts, event
}

// PerfRecord runs perf record for a specific process.
type PerfRecorder struct {
	cmd     *exec.Cmd
	output  *bytes.Buffer
	outFile string
}

// NewPerfRecorder creates a new perf recorder targeting a specific PID.
func NewPerfRecorder(pid int, events []string) (*PerfRecorder, error) {
	outFile := fmt.Sprintf("/tmp/perf-kvm-%d.data", pid)

	eventArgs := make([]string, 0, len(events)*2)
	for _, e := range events {
		eventArgs = append(eventArgs, "-e", e)
	}

	args := append([]string{"record", "-p", strconv.Itoa(pid), "-o", outFile}, eventArgs...)
	cmd := exec.Command("perf", args...)

	var output bytes.Buffer
	cmd.Stderr = &output

	return &PerfRecorder{
		cmd:     cmd,
		output:  &output,
		outFile: outFile,
	}, nil
}

// Start begins perf recording.
func (p *PerfRecorder) Start() error {
	return p.cmd.Start()
}

// Stop stops perf recording and returns the output file path.
func (p *PerfRecorder) Stop() (string, error) {
	if p.cmd.Process != nil {
		p.cmd.Process.Signal(os.Interrupt)
		p.cmd.Wait()
	}
	return p.outFile, nil
}

// GetKVMGuestState retrieves the current state of KVM guests.
func GetKVMGuestState() (string, error) {
	// Read from /sys/kernel/debug/kvm if available
	kvmDir := "/sys/kernel/debug/kvm"
	if _, err := os.Stat(kvmDir); os.IsNotExist(err) {
		return "KVM debug directory not available", nil
	}

	entries, err := os.ReadDir(kvmDir)
	if err != nil {
		return "", err
	}

	var result strings.Builder
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), "vm") {
			vmPath := filepath.Join(kvmDir, entry.Name())

			// Try to read vcpu states
			vcpuFiles, _ := filepath.Glob(filepath.Join(vmPath, "vcpu*"))
			for _, vcpuFile := range vcpuFiles {
				content, err := os.ReadFile(vcpuFile)
				if err == nil {
					result.WriteString(fmt.Sprintf("--- %s ---\n%s\n", vcpuFile, string(content)))
				}
			}
		}
	}

	return result.String(), nil
}

// DmesgKVMEvents returns recent KVM-related dmesg entries.
func DmesgKVMEvents(since time.Duration) (string, error) {
	cmd := exec.Command("dmesg", "-T", "--level=info,warn,err")
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}

	var result strings.Builder
	cutoff := time.Now().Add(-since)

	for _, line := range strings.Split(string(output), "\n") {
		// Look for KVM or virt-related entries
		if strings.Contains(strings.ToLower(line), "kvm") ||
			strings.Contains(strings.ToLower(line), "virt") ||
			strings.Contains(strings.ToLower(line), "firecracker") {
			result.WriteString(line)
			result.WriteString("\n")
		}
		// Also include recent entries (simple time parsing)
		if len(line) > 0 && line[0] == '[' {
			// Skip time filtering for now - include all KVM lines
			_ = cutoff
		}
	}

	return result.String(), nil
}
