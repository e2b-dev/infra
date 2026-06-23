//go:build linux

package orphan

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/process"
	"github.com/vishvananda/netlink"
)

// fcSocketPattern matches Firecracker API socket filenames:
//
//	fc-<sandboxID>-<randomID>.sock
var fcSocketPattern = regexp.MustCompile(`^fc-[a-z0-9]+-[a-z0-9]+\.sock$`)

// fcMetricsFIFOPattern matches Firecracker metrics FIFO filenames:
//
//	fc-metrics-<sandboxID>-<randomID>.fifo
var fcMetricsFIFOPattern = regexp.MustCompile(`^fc-metrics-[a-z0-9]+-[a-z0-9]+\.fifo$`)

// vethPattern matches host-side veth interface names created per network slot:
//
//	veth-<idx>
var vethPattern = regexp.MustCompile(`^veth-(\d+)$`)

// firecrackerBinaryPattern matches any Firecracker binary path under the
// versioned directory, e.g. /fc-versions/v1.12.1_210cbac/firecracker
var firecrackerBinaryPattern = regexp.MustCompile(`/firecracker$`)

// scanOrphanedProcesses returns all Firecracker processes whose PPID is 1
// (adopted by init) and that have been running for at least minAge.
//
// Processes whose PPID matches orchestratorPID are considered live and are
// excluded from the result.
func scanOrphanedProcesses(orchestratorPID int32, minAge time.Duration) ([]OrphanedProcess, error) {
	procs, err := process.Processes()
	if err != nil {
		return nil, fmt.Errorf("listing processes: %w", err)
	}

	now := time.Now()
	var orphans []OrphanedProcess

	for _, p := range procs {
		exe, err := p.Exe()
		if err != nil {
			// Process may have exited between listing and inspection.
			continue
		}

		if !firecrackerBinaryPattern.MatchString(exe) {
			continue
		}

		ppid, err := p.Ppid()
		if err != nil {
			continue
		}

		// Skip processes still parented by the orchestrator.
		if ppid == orchestratorPID {
			continue
		}

		// Only target processes adopted by init (PPID == 1).
		if ppid != 1 {
			continue
		}

		// Check process age: skip processes younger than minAge.
		createTime, err := p.CreateTime()
		if err != nil {
			continue
		}

		startedAt := time.UnixMilli(createTime)
		if now.Sub(startedAt) < minAge {
			continue
		}

		// Extract --api-sock argument from the command line.
		cmdline, err := p.CmdlineSlice()
		if err != nil {
			continue
		}

		socketPath := extractAPISocket(cmdline)

		orphans = append(orphans, OrphanedProcess{
			PID:        p.Pid,
			PPID:       ppid,
			SocketPath: socketPath,
			DetectedAt: now,
		})
	}

	return orphans, nil
}

// extractAPISocket returns the value of the --api-sock flag from a Firecracker
// command-line slice, or an empty string if not found.
func extractAPISocket(args []string) string {
	for i, arg := range args {
		if arg == "--api-sock" && i+1 < len(args) {
			return args[i+1]
		}

		// Also handle --api-sock=<path> form.
		if strings.HasPrefix(arg, "--api-sock=") {
			return strings.TrimPrefix(arg, "--api-sock=")
		}
	}

	return ""
}

// scanOrphanedSockets scans tmpDirs for fc-*.sock files that have no
// corresponding live Firecracker process.
func scanOrphanedSockets(tmpDirs []string, liveSockets map[string]struct{}) ([]OrphanedSocket, error) {
	now := time.Now()
	var orphans []OrphanedSocket

	for _, dir := range tmpDirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}

			return nil, fmt.Errorf("reading dir %s: %w", dir, err)
		}

		for _, e := range entries {
			if e.IsDir() {
				continue
			}

			if !fcSocketPattern.MatchString(e.Name()) {
				continue
			}

			fullPath := filepath.Join(dir, e.Name())
			if _, alive := liveSockets[fullPath]; alive {
				continue
			}

			orphans = append(orphans, OrphanedSocket{
				Path:       fullPath,
				DetectedAt: now,
			})
		}
	}

	return orphans, nil
}

// scanOrphanedFIFOs scans tmpDirs for fc-metrics-*.fifo files that have no
// corresponding live Firecracker process.
func scanOrphanedFIFOs(tmpDirs []string, liveSockets map[string]struct{}) ([]OrphanedFIFO, error) {
	now := time.Now()
	var orphans []OrphanedFIFO

	for _, dir := range tmpDirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}

			return nil, fmt.Errorf("reading dir %s: %w", dir, err)
		}

		for _, e := range entries {
			if e.IsDir() {
				continue
			}

			if !fcMetricsFIFOPattern.MatchString(e.Name()) {
				continue
			}

			// Derive the corresponding socket name from the FIFO name:
			// fc-metrics-<id>-<rid>.fifo → fc-<id>-<rid>.sock
			sockName := fifoNameToSocketName(e.Name())
			if sockName != "" {
				// Check any of the tmpDirs for the socket.
				socketAlive := false

				for _, d := range tmpDirs {
					if _, alive := liveSockets[filepath.Join(d, sockName)]; alive {
						socketAlive = true

						break
					}
				}

				if socketAlive {
					continue
				}
			}

			orphans = append(orphans, OrphanedFIFO{
				Path:       filepath.Join(dir, e.Name()),
				DetectedAt: now,
			})
		}
	}

	return orphans, nil
}

// fifoNameToSocketName converts "fc-metrics-<id>-<rid>.fifo" to
// "fc-<id>-<rid>.sock".  Returns "" if the name does not match the expected
// pattern.
func fifoNameToSocketName(fifoName string) string {
	// fc-metrics-<id>-<rid>.fifo
	trimmed := strings.TrimSuffix(fifoName, ".fifo")
	if !strings.HasPrefix(trimmed, "fc-metrics-") {
		return ""
	}

	rest := strings.TrimPrefix(trimmed, "fc-metrics-")

	return "fc-" + rest + ".sock"
}

// scanOrphanedVeths returns all veth-N interfaces on the host that have no
// corresponding entry in liveSlotIdxs.
func scanOrphanedVeths(liveSlotIdxs map[int]struct{}) ([]OrphanedVeth, error) {
	links, err := netlink.LinkList()
	if err != nil {
		return nil, fmt.Errorf("listing netlink interfaces: %w", err)
	}

	now := time.Now()
	var orphans []OrphanedVeth

	for _, link := range links {
		name := link.Attrs().Name
		m := vethPattern.FindStringSubmatch(name)

		if m == nil {
			continue
		}

		idx, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}

		if _, alive := liveSlotIdxs[idx]; alive {
			continue
		}

		orphans = append(orphans, OrphanedVeth{
			Name:       name,
			SlotIdx:    idx,
			DetectedAt: now,
		})
	}

	return orphans, nil
}

// buildLiveSockets returns a set of socket paths currently referenced by live
// Firecracker processes (any PPID, not just orchestrator children).
func buildLiveSockets() (map[string]struct{}, error) {
	procs, err := process.Processes()
	if err != nil {
		return nil, fmt.Errorf("listing processes: %w", err)
	}

	live := make(map[string]struct{})

	for _, p := range procs {
		exe, err := p.Exe()
		if err != nil {
			continue
		}

		if !firecrackerBinaryPattern.MatchString(exe) {
			continue
		}

		cmdline, err := p.CmdlineSlice()
		if err != nil {
			continue
		}

		if sock := extractAPISocket(cmdline); sock != "" {
			live[sock] = struct{}{}
		}
	}

	return live, nil
}
