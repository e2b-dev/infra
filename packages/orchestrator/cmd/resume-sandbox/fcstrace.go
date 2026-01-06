package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// FCStraceTracer attaches strace to the Firecracker process to trace syscalls.
type FCStraceTracer struct {
	pid       int
	cmd       *exec.Cmd
	output    bytes.Buffer
	startTime time.Time
}

// NewFCStraceTracer creates a new strace tracer for the given FC process PID.
func NewFCStraceTracer(pid int) *FCStraceTracer {
	return &FCStraceTracer{
		pid: pid,
	}
}

// Start begins tracing syscalls on the FC process.
// It traces all threads (-f) and captures timing (-T) for blocking syscalls.
func (t *FCStraceTracer) Start(ctx context.Context) error {
	// Use strace to trace the FC process
	// -f: follow threads
	// -T: show time spent in syscall
	// -tt: print timestamps with microseconds
	// -e trace=all: capture ALL syscalls to see what's happening
	// -p: attach to PID
	t.cmd = exec.CommandContext(ctx,
		"strace",
		"-f",        // follow threads
		"-T",        // show time spent in syscall
		"-tt",       // timestamps with microseconds
		"-yy",       // decode file descriptor paths and socket addresses
		"-s", "256", // longer string length to see more context
		"-e", "trace=all", // ALL syscalls to see everything
		"-p", fmt.Sprintf("%d", t.pid),
	)

	t.cmd.Stdout = &t.output
	t.cmd.Stderr = &t.output // strace writes to stderr

	// Don't kill child processes when the parent dies
	t.cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}

	t.startTime = time.Now()
	fmt.Printf("  [strace] Attaching to FC pid %d...\n", t.pid)

	if err := t.cmd.Start(); err != nil {
		return fmt.Errorf("failed to start strace: %w", err)
	}

	// Give strace a moment to attach
	time.Sleep(20 * time.Millisecond)
	fmt.Printf("  [strace] Attached, tracing syscalls...\n")

	return nil
}

// Stop stops the strace process and returns the captured output.
func (t *FCStraceTracer) Stop() (string, error) {
	if t.cmd == nil || t.cmd.Process == nil {
		return "", fmt.Errorf("strace not started")
	}

	elapsed := time.Since(t.startTime)
	fmt.Printf("  [strace] Stopping after %s, captured %d bytes...\n", elapsed, t.output.Len())

	// Send SIGINT to strace to stop it gracefully
	if err := t.cmd.Process.Signal(syscall.SIGINT); err != nil {
		// Process might have already exited
		if !strings.Contains(err.Error(), "process already finished") {
			// Try SIGTERM
			t.cmd.Process.Signal(syscall.SIGTERM)
		}
	}

	// Wait for strace to finish (with timeout)
	done := make(chan error, 1)
	go func() {
		done <- t.cmd.Wait()
	}()

	select {
	case <-done:
		// Process exited
	case <-time.After(2 * time.Second):
		t.cmd.Process.Kill()
		<-done
	}

	output := t.output.String()

	// Print first 20 lines for debugging
	lines := strings.Split(output, "\n")
	fmt.Printf("  [strace] Captured %d lines. First lines:\n", len(lines))
	for i := 0; i < min(20, len(lines)); i++ {
		if lines[i] != "" {
			fmt.Printf("    %s\n", lines[i])
		}
	}
	if len(lines) > 20 {
		fmt.Printf("    ... (%d more lines)\n", len(lines)-20)
	}

	return output, nil
}

// Duration returns how long strace was running.
func (t *FCStraceTracer) Duration() time.Duration {
	return time.Since(t.startTime)
}

// StraceStats contains parsed strace statistics.
type StraceStats struct {
	TotalSyscalls     int
	BlockingSyscalls  int // Syscalls that took > 100µs (meaningful blocking)
	TotalBlockingTime time.Duration
	TotalTime         time.Duration  // Total time spent in all syscalls
	TopSyscalls       []SyscallStat  // Top syscalls by count
	TopBlockers       []SyscallStat  // Top syscalls by total blocking time
	LongestCalls      []SyscallEvent // Individual longest calls
	EpollWaits        int
	Reads             int
	Writes            int
	NetworkCalls      int
	FutexWaits        int
	TimelineEvents    []SyscallEvent // Chronological list for visualization
	// Ioctl breakdown
	IoctlStats        []IoctlTypeStat // Breakdown of ioctl types
}

// IoctlTypeStat contains statistics for a specific ioctl type.
type IoctlTypeStat struct {
	Type      string        // KVM, Network, Virtio, Terminal, Other
	Command   string        // Specific command (e.g., KVM_RUN, SIOCGIFADDR)
	Count     int
	TotalTime time.Duration
	MaxTime   time.Duration
}

// SyscallStat contains statistics for a syscall type.
type SyscallStat struct {
	Name         string
	Count        int
	TotalTime    time.Duration
	MaxTime      time.Duration
	AvgTime      time.Duration
	BlockingPct  float64 // Percentage of total blocking time
}

// SyscallEvent represents a single syscall with timing.
type SyscallEvent struct {
	Timestamp   time.Time
	TimestampUs float64 // Microseconds since trace start
	PID         int
	Syscall     string
	Args        string
	Result      string
	Duration    time.Duration
	FD          string // File descriptor info if available
}

// ParseStraceOutput parses strace output and returns statistics.
func ParseStraceOutput(output string, traceStart time.Time) *StraceStats {
	stats := &StraceStats{
		TopBlockers:    make([]SyscallStat, 0),
		LongestCalls:   make([]SyscallEvent, 0),
		TimelineEvents: make([]SyscallEvent, 0),
	}

	syscallCounts := make(map[string]*SyscallStat)
	ioctlCounts := make(map[string]*IoctlTypeStat) // key = "Type:Command"

	// Parse strace output line by line
	// Format examples:
	// [pid 12345] 05:00:56.360324 epoll_pwait(9<...>, ...) = 1 <0.017824>
	// 05:00:56.360324 write(5, "data", 4) = 4 <0.000011>
	// [pid 12345] 05:00:56.360324 ioctl(19<anon_inode:kvm-vcpu:0>, KVM_RUN <unfinished ...>
	// [pid 12345] 05:00:56.379304 <... ioctl resumed>, 0) = 0 <0.019223>

	// Regex for: optional [pid N], timestamp, syscall name
	headerRegex := regexp.MustCompile(`^(?:\[pid\s+(\d+)\]\s+)?(\d{2}:\d{2}:\d{2}\.\d+)\s+<?\.\.\.?\s*(\w+)?\s*resumed>?,?\s*|^(?:\[pid\s+(\d+)\]\s+)?(\d{2}:\d{2}:\d{2}\.\d+)\s+(\w+)\(`)

	// Regex for duration at end of line: <0.017824>
	durationRegex := regexp.MustCompile(`<(\d+\.\d+)>\s*$`)

	// Regex for result: = N or = 0xABC or = ?
	resultRegex := regexp.MustCompile(`=\s*(-?\d+|0x[0-9a-fA-F]+|\?|-\d+\s+\w+)`)

	scanner := bufio.NewScanner(strings.NewReader(output))
	var firstTimestamp time.Time
	lineNum := 0

	for scanner.Scan() {
		line := scanner.Text()
		lineNum++

		// Skip non-syscall lines (strace: messages, etc)
		if strings.HasPrefix(line, "strace:") || strings.Contains(line, "<detached") {
			continue
		}

		// Extract duration from end of line first (most reliable)
		var duration time.Duration
		if match := durationRegex.FindStringSubmatch(line); match != nil {
			if secs, err := strconv.ParseFloat(match[1], 64); err == nil {
				duration = time.Duration(secs * float64(time.Second))
			}
		}

		// Try to parse the header
		matches := headerRegex.FindStringSubmatch(line)
		if matches == nil {
			continue
		}

		// Extract components based on which capture groups matched
		var pid int
		var timestampStr, syscallName string

		if matches[3] != "" {
			// This is a "resumed" line
			if matches[1] != "" {
				pid, _ = strconv.Atoi(matches[1])
			}
			timestampStr = matches[2]
			syscallName = matches[3]
		} else if matches[6] != "" {
			// This is a regular syscall line
			if matches[4] != "" {
				pid, _ = strconv.Atoi(matches[4])
			}
			timestampStr = matches[5]
			syscallName = matches[6]
		} else {
			continue
		}

		// Skip unfinished calls (we'll get the duration from the resumed line)
		if strings.Contains(line, "<unfinished") {
			continue
		}

		stats.TotalSyscalls++

		// Parse timestamp
		var ts time.Time
		if t, err := time.Parse("15:04:05.000000", timestampStr); err == nil {
			ts = t
		} else if t, err := time.Parse("15:04:05.0000", timestampStr); err == nil {
			ts = t
		}

		// Extract result
		var result string
		if match := resultRegex.FindStringSubmatch(line); match != nil {
			result = match[1]
		}

		if firstTimestamp.IsZero() && !ts.IsZero() {
			firstTimestamp = ts
		}

		// Calculate offset from trace start
		var offsetUs float64
		if !firstTimestamp.IsZero() && !ts.IsZero() {
			// Get the duration between timestamps (ignoring date)
			h1, m1, s1 := firstTimestamp.Clock()
			ns1 := firstTimestamp.Nanosecond()
			h2, m2, s2 := ts.Clock()
			ns2 := ts.Nanosecond()

			t1 := time.Duration(h1)*time.Hour + time.Duration(m1)*time.Minute + time.Duration(s1)*time.Second + time.Duration(ns1)*time.Nanosecond
			t2 := time.Duration(h2)*time.Hour + time.Duration(m2)*time.Minute + time.Duration(s2)*time.Second + time.Duration(ns2)*time.Nanosecond

			offsetUs = float64(t2-t1) / float64(time.Microsecond)
		}

		// Extract FD info from the line
		fd := extractFDInfo(line)

		event := SyscallEvent{
			Timestamp:   ts,
			TimestampUs: offsetUs,
			PID:         pid,
			Syscall:     syscallName,
			Args:        truncateString(line, 150),
			Result:      result,
			Duration:    duration,
			FD:          fd,
		}

		// Track syscall stats
		if _, ok := syscallCounts[syscallName]; !ok {
			syscallCounts[syscallName] = &SyscallStat{Name: syscallName}
		}
		stat := syscallCounts[syscallName]
		stat.Count++
		stat.TotalTime += duration
		if duration > stat.MaxTime {
			stat.MaxTime = duration
		}

		// Categorize syscalls
		switch {
		case strings.HasPrefix(syscallName, "epoll"):
			stats.EpollWaits++
		case syscallName == "read" || syscallName == "pread64" || syscallName == "readv":
			stats.Reads++
		case syscallName == "write" || syscallName == "pwrite64" || syscallName == "writev":
			stats.Writes++
		case syscallName == "futex":
			stats.FutexWaits++
		case isNetworkSyscall(syscallName):
			stats.NetworkCalls++
		case syscallName == "ioctl":
			// Parse ioctl type and command from the line
			ioctlType, ioctlCmd := parseIoctlInfo(line)
			key := ioctlType + ":" + ioctlCmd
			if _, ok := ioctlCounts[key]; !ok {
				ioctlCounts[key] = &IoctlTypeStat{Type: ioctlType, Command: ioctlCmd}
			}
			ioctlCounts[key].Count++
			ioctlCounts[key].TotalTime += duration
			if duration > ioctlCounts[key].MaxTime {
				ioctlCounts[key].MaxTime = duration
			}
		}

		// Track total time
		stats.TotalTime += duration

		// Track blocking syscalls (> 100µs - meaningful blocking)
		if duration > 100*time.Microsecond {
			stats.BlockingSyscalls++
			stats.TotalBlockingTime += duration
		}

		// Track significant calls (> 1ms) for the per-run table
		if duration > time.Millisecond {
			stats.LongestCalls = append(stats.LongestCalls, event)
		}

		// Add to timeline
		stats.TimelineEvents = append(stats.TimelineEvents, event)
	}

	// Build ioctl stats list sorted by total time
	var ioctlStatsList []IoctlTypeStat
	for _, stat := range ioctlCounts {
		ioctlStatsList = append(ioctlStatsList, *stat)
	}
	sort.Slice(ioctlStatsList, func(i, j int) bool {
		return ioctlStatsList[i].TotalTime > ioctlStatsList[j].TotalTime
	})
	if len(ioctlStatsList) > 10 {
		stats.IoctlStats = ioctlStatsList[:10]
	} else {
		stats.IoctlStats = ioctlStatsList
	}

	// Build stats list with calculated averages and percentages
	var statsList []SyscallStat
	for _, stat := range syscallCounts {
		if stat.Count > 0 {
			stat.AvgTime = stat.TotalTime / time.Duration(stat.Count)
		}
		// Calculate percentage of total syscall time (not just blocking)
		if stats.TotalTime > 0 {
			stat.BlockingPct = float64(stat.TotalTime) / float64(stats.TotalTime) * 100
		}
		statsList = append(statsList, *stat)
	}

	// TopSyscalls: sorted by count (what syscalls happen most)
	byCount := make([]SyscallStat, len(statsList))
	copy(byCount, statsList)
	sort.Slice(byCount, func(i, j int) bool {
		return byCount[i].Count > byCount[j].Count
	})
	if len(byCount) > 10 {
		stats.TopSyscalls = byCount[:10]
	} else {
		stats.TopSyscalls = byCount
	}

	// TopBlockers: sorted by total time (what syscalls take most time)
	sort.Slice(statsList, func(i, j int) bool {
		return statsList[i].TotalTime > statsList[j].TotalTime
	})
	if len(statsList) > 10 {
		stats.TopBlockers = statsList[:10]
	} else {
		stats.TopBlockers = statsList
	}

	// Sort significant calls (>1ms) by timestamp for timeline correlation
	sort.Slice(stats.LongestCalls, func(i, j int) bool {
		return stats.LongestCalls[i].TimestampUs < stats.LongestCalls[j].TimestampUs
	})

	return stats
}

func isNetworkSyscall(name string) bool {
	networkCalls := map[string]bool{
		"socket": true, "connect": true, "accept": true, "accept4": true,
		"sendto": true, "recvfrom": true, "sendmsg": true, "recvmsg": true,
		"bind": true, "listen": true, "getsockname": true, "getpeername": true,
		"socketpair": true, "setsockopt": true, "getsockopt": true,
		"shutdown": true, "sendmmsg": true, "recvmmsg": true,
	}
	return networkCalls[name]
}

func extractFDInfo(line string) string {
	// Look for fd patterns like "5<socket:[12345]>" or "3</path/to/file>"
	// Also extract the type of fd for quick identification
	fdRegex := regexp.MustCompile(`(\d+)<([^>]+)>`)
	matches := fdRegex.FindAllStringSubmatch(line, -1)
	if len(matches) == 0 {
		return ""
	}

	var parts []string
	for _, match := range matches {
		fdNum := match[1]
		fdDesc := match[2]

		// Categorize the fd
		var fdType string
		switch {
		case strings.Contains(fdDesc, "socket"):
			fdType = "socket"
		case strings.Contains(fdDesc, "kvm"):
			fdType = "kvm"
		case strings.Contains(fdDesc, "tun") || strings.Contains(fdDesc, "tap"):
			fdType = "net-tap"
		case strings.Contains(fdDesc, "eventfd"):
			fdType = "eventfd"
		case strings.Contains(fdDesc, "eventpoll"):
			fdType = "epoll"
		case strings.Contains(fdDesc, "vsock"):
			fdType = "vsock"
		case strings.Contains(fdDesc, "/dev/"):
			fdType = "dev"
		case strings.HasPrefix(fdDesc, "/"):
			fdType = "file"
		case strings.Contains(fdDesc, "anon_inode"):
			fdType = "anon"
		default:
			fdType = "?"
		}

		// Shorten the description
		shortDesc := fdDesc
		if len(shortDesc) > 30 {
			shortDesc = shortDesc[:27] + "..."
		}

		parts = append(parts, fmt.Sprintf("[%s:%s] %s", fdType, fdNum, shortDesc))
	}

	return strings.Join(parts, " ")
}

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

// parseIoctlInfo extracts the ioctl type and command from a strace line.
// Example: ioctl(19<anon_inode:kvm-vcpu:0>, KVM_RUN, 0) = 0
// Returns: ("KVM", "KVM_RUN")
func parseIoctlInfo(line string) (ioctlType, ioctlCmd string) {
	ioctlType = "Other"
	ioctlCmd = "unknown"

	// Try to extract the fd info to determine device type
	fdRegex := regexp.MustCompile(`ioctl\(\d+<([^>]+)>`)
	if match := fdRegex.FindStringSubmatch(line); match != nil {
		fdInfo := match[1]
		switch {
		case strings.Contains(fdInfo, "kvm"):
			ioctlType = "KVM"
		case strings.Contains(fdInfo, "vhost"):
			ioctlType = "Virtio"
		case strings.Contains(fdInfo, "tun") || strings.Contains(fdInfo, "tap"):
			ioctlType = "Network"
		case strings.Contains(fdInfo, "socket"):
			ioctlType = "Network"
		case strings.Contains(fdInfo, "/dev/"):
			ioctlType = "Device"
		case strings.Contains(fdInfo, "eventfd"):
			ioctlType = "EventFD"
		case strings.Contains(fdInfo, "eventpoll"):
			ioctlType = "Epoll"
		}
	}

	// Try to extract the ioctl command
	// Pattern: ioctl(..., COMMAND, ...) or ioctl(..., COMMAND)
	cmdRegex := regexp.MustCompile(`ioctl\([^,]+,\s*([A-Z_0-9]+)`)
	if match := cmdRegex.FindStringSubmatch(line); match != nil {
		ioctlCmd = match[1]
		// Override type based on command name if more specific
		switch {
		case strings.HasPrefix(ioctlCmd, "KVM_"):
			ioctlType = "KVM"
		case strings.HasPrefix(ioctlCmd, "VHOST_"):
			ioctlType = "Virtio"
		case strings.HasPrefix(ioctlCmd, "SIOC"):
			ioctlType = "Network"
		case strings.HasPrefix(ioctlCmd, "TIOC") || strings.HasPrefix(ioctlCmd, "TC"):
			ioctlType = "Terminal"
		case strings.HasPrefix(ioctlCmd, "FION"):
			ioctlType = "File"
		case strings.HasPrefix(ioctlCmd, "FS_IOC"):
			ioctlType = "Filesystem"
		}
	}

	return ioctlType, ioctlCmd
}
