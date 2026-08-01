// portf (port forward) periodaically scans opened TCP ports on the 127.0.0.1 (or localhost)
// and launches `socat` process for every such port in the background.
// socat forward traffic from `sourceIP`:port to the 127.0.0.1:port.

// WARNING: portf isn't thread safe!

package port

import (
	"context"
	"fmt"
	"net"
	"os/exec"
	"sync"
	"syscall"

	"github.com/rs/zerolog"
	gopsnet "github.com/shirou/gopsutil/v4/net"

	"github.com/e2b-dev/infra/packages/envd/internal/services/cgroups"
	"github.com/e2b-dev/infra/packages/envd/internal/services/spec/upgrade"
)

type PortState string

const (
	PortStateForward PortState = "FORWARD"
	PortStateDelete  PortState = "DELETE"
)

var defaultGatewayIP = net.IPv4(169, 254, 0, 21)

type PortToForward struct {
	socat *exec.Cmd
	// socatPid is the pid of a socat re-adopted across a live-upgrade, for which
	// there is no *exec.Cmd (the old envd's runtime — and its Wait goroutine —
	// were replaced by execve). 0 for socats this envd spawned itself.
	socatPid int
	// Process ID of the process that's listening on port.
	pid int32
	// family version of the ip.
	family uint32
	state  PortState
	port   uint32
}

// socatPID returns the pid of the forwarding socat regardless of whether it was
// spawned by this envd (*exec.Cmd) or re-adopted across a live-upgrade (pid
// only). Returns 0 when there is no socat.
func (p *PortToForward) socatPID() int {
	if p.socat != nil && p.socat.Process != nil {
		return p.socat.Process.Pid
	}

	return p.socatPid
}

type Forwarder struct {
	logger        *zerolog.Logger
	cgroupManager cgroups.Manager
	// mu guards the ports map. The scan loop is single-goroutine, but the
	// live-upgrade export (ExportForwards) reads the map from the upgrade
	// goroutine concurrently, so map access is serialized.
	mu sync.Mutex
	// Map of ports that are being currently forwarded.
	ports             map[string]*PortToForward
	scannerSubscriber *ScannerSubscriber
	sourceIP          net.IP
}

func NewForwarder(
	logger *zerolog.Logger,
	scanner *Scanner,
	cgroupManager cgroups.Manager,
) *Forwarder {
	scannerSub := scanner.AddSubscriber(
		logger,
		"port-forwarder",
		// We only want to forward ports that are actively listening on localhost.
		&ScannerFilter{
			IPs:   []string{"127.0.0.1", "localhost", "::1"},
			State: "LISTEN",
		},
	)

	return &Forwarder{
		logger:            logger,
		sourceIP:          defaultGatewayIP,
		ports:             make(map[string]*PortToForward),
		scannerSubscriber: scannerSub,
		cgroupManager:     cgroupManager,
	}
}

func (f *Forwarder) StartForwarding(ctx context.Context) {
	if f.scannerSubscriber == nil {
		f.logger.Error().Msg("Cannot start forwarding because scanner subscriber is nil")

		return
	}

	for {
		// Wait for the next scan result or context cancellation.
		select {
		case <-ctx.Done():
			return
		case procs, ok := <-f.scannerSubscriber.Messages:
			if !ok {
				return
			}

			f.refresh(ctx, procs)
		}
	}
}

// refresh reconciles the forwarded ports with the listening sockets reported by
// the latest scan.
func (f *Forwarder) refresh(ctx context.Context, procs []gopsnet.ConnectionStat) {
	// Serialize the whole refresh against a concurrent ExportForwards
	// (live-upgrade). stop/startPortForwarding below are called with the
	// lock held and must not take it themselves.
	f.mu.Lock()
	defer f.mu.Unlock()

	// Now we are going to refresh all ports that are being forwarded in the `ports` map. Maybe add new ones
	// and maybe remove some.

	// Go through the ports that are currently being forwarded and set all of them
	// to the `DELETE` state. We don't know yet if they will be there after refresh.
	for _, v := range f.ports {
		v.state = PortStateDelete
	}

	// Let's refresh our map of currently forwarded ports and mark the currently opened ones with the "FORWARD" state.
	// This will make sure we won't delete them later.
	// The actual socat process that handles forwarding is running from the last iteration.
	for _, p := range procs {
		if val, portOk := f.ports[forwardKey(p)]; portOk {
			val.state = PortStateForward
		}
	}

	// Stop forwarding all ports that stayed marked as "DELETE" and forget them.
	// Forgetting is what makes the forwarding recoverable: a retained entry would
	// be marked "FORWARD" again as soon as the same listener shows up in a later
	// scan, and because it is already in the map no socat would ever be started
	// for it again — leaving the port permanently unreachable from outside the
	// sandbox. Listeners do drop out of individual scans: an app can close and
	// reopen its socket, and the scanner reports a socket with no pid when it
	// cannot map the socket's inode to a process.
	//
	// This also has to happen before the new forwards are started below, so a
	// fresh socat never has to contend for a bind address that a socat being
	// torn down still holds.
	for key, v := range f.ports {
		if v.state == PortStateDelete {
			f.stopPortForwarding(v)
			delete(f.ports, key)
		}
	}

	// Start forwarding the ports that are newly opened.
	for _, p := range procs {
		key := forwardKey(p)
		if _, portOk := f.ports[key]; portOk {
			continue
		}

		f.logger.Debug().
			Str("ip", p.Laddr.IP).
			Uint32("port", p.Laddr.Port).
			Uint32("family", familyToIPVersion(p.Family)).
			Str("state", p.Status).
			Msg("Detected new opened port on localhost that is not forwarded")

		ptf := &PortToForward{
			pid:    p.Pid,
			port:   p.Laddr.Port,
			state:  PortStateForward,
			family: familyToIPVersion(p.Family),
		}
		f.ports[key] = ptf
		f.startPortForwarding(ctx, ptf)
	}
}

func forwardKey(p gopsnet.ConnectionStat) string {
	return fmt.Sprintf("%d-%d", p.Pid, p.Laddr.Port)
}

func (f *Forwarder) startPortForwarding(ctx context.Context, p *PortToForward) {
	// https://unix.stackexchange.com/questions/311492/redirect-application-listening-on-localhost-to-listening-on-external-interface
	// socat -d -d TCP4-LISTEN:4000,bind=169.254.0.21,fork TCP4:localhost:4000
	// reuseaddr is used to fix the "Address already in use" error when restarting socat quickly.
	cmd := exec.CommandContext(ctx,
		"socat", "-d", "-d", "-d",
		fmt.Sprintf("TCP4-LISTEN:%v,bind=%s,reuseaddr,fork", p.port, f.sourceIP.To4()),
		fmt.Sprintf("TCP%d:localhost:%v", p.family, p.port),
	)

	cgroupFD, ok := f.cgroupManager.GetFileDescriptor(cgroups.ProcessTypeSocat)

	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}
	applyCgroupFD(cmd.SysProcAttr, cgroupFD, ok)

	f.logger.Debug().
		Str("socatCmd", cmd.String()).
		Int32("pid", p.pid).
		Uint32("family", p.family).
		IPAddr("sourceIP", f.sourceIP.To4()).
		Uint32("port", p.port).
		Msg("About to start port forwarding")

	if err := cmd.Start(); err != nil {
		f.logger.
			Error().
			Str("socatCmd", cmd.String()).
			Err(err).
			Msg("Failed to start port forwarding - failed to start socat")

		return
	}

	go func() {
		if err := cmd.Wait(); err != nil {
			f.logger.
				Debug().
				Str("socatCmd", cmd.String()).
				Err(err).
				Msg("Port forwarding socat process exited")
		}
	}()

	p.socat = cmd
}

func (f *Forwarder) stopPortForwarding(p *PortToForward) {
	pid := p.socatPID()
	if pid <= 0 {
		return
	}

	defer func() { p.socat = nil; p.socatPid = 0 }()

	logger := f.logger.With().
		Int("socatPid", pid).
		Int32("pid", p.pid).
		Uint32("family", p.family).
		IPAddr("sourceIP", f.sourceIP.To4()).
		Uint32("port", p.port).
		Logger()

	logger.Debug().Msg("Stopping port forwarding")

	// Kill the socat's process group. A re-adopted socat has no *exec.Cmd Wait
	// goroutine to reap it, so ImportForwards started a wait4 reaper for it; a
	// self-spawned socat is reaped by its startPortForwarding Wait goroutine.
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil {
		logger.Error().Err(err).Msg("Failed to kill process group")

		return
	}

	logger.Debug().Msg("Stopped port forwarding")
}

// ExportForwards snapshots the active port-forwards as typed ForwardedPort
// messages, carried across an envd live-upgrade. The socat children survive the
// same-PID execve; carrying their pids lets the new forwarder re-adopt them
// (ImportForwards) instead of spawning duplicate socats that would contend on
// the same bind address and leak the originals as un-reaped zombies.
func (f *Forwarder) ExportForwards() []*upgrade.ForwardedPort {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.exportLocked()
}

// ExportForwardsHold is ExportForwards but keeps the forwarder mutex held,
// returning a release func the caller invokes to resume scanning. The
// live-upgrade path holds it from the snapshot through the execve so the scan
// loop cannot spawn a new socat in that window — a socat started after the
// snapshot would be orphaned by the swap (never carried, never re-adopted) or
// duplicate a port the new envd re-adopts, contending on the same bind address.
// A successful execve replaces this process and the held lock vanishes with it;
// on failure the caller's release restores scanning under the old envd.
func (f *Forwarder) ExportForwardsHold() ([]*upgrade.ForwardedPort, func()) {
	f.mu.Lock()

	return f.exportLocked(), f.mu.Unlock
}

// exportLocked snapshots the active port-forwards. The caller must hold f.mu.
func (f *Forwarder) exportLocked() []*upgrade.ForwardedPort {
	out := make([]*upgrade.ForwardedPort, 0, len(f.ports))
	for key, p := range f.ports {
		pid := p.socatPID()
		if pid <= 0 {
			continue
		}
		out = append(out, &upgrade.ForwardedPort{
			Key:         key,
			Port:        p.port,
			ListenerPid: p.pid,
			Family:      p.family,
			SocatPid:    int32(pid),
		})
	}

	return out
}

// ImportForwards re-adopts the socats carried across a live-upgrade: it seeds the
// ports map so the next scan recognizes each already-forwarded port (and does
// not spawn a duplicate socat), and starts a reaper for each re-adopted socat
// (there is no surviving *exec.Cmd Wait goroutine for it). It MUST run before
// StartForwarding so the seeding is race-free. A socat that did not survive the
// handover is skipped, so the next scan respawns a fresh one for the still-open
// port.
func (f *Forwarder) ImportForwards(forwards []*upgrade.ForwardedPort) (readopted int) {
	f.mu.Lock()
	defer f.mu.Unlock()

	for _, fp := range forwards {
		pid := int(fp.GetSocatPid())
		if pid <= 0 {
			continue
		}
		// Only re-adopt a socat that is still alive (kill -0). One that exited
		// during the handover window is left out so the next scan respawns rather
		// than recording a dead socat as "forwarded".
		if syscall.Kill(pid, 0) != nil {
			continue
		}
		f.ports[fp.GetKey()] = &PortToForward{
			pid:      fp.GetListenerPid(),
			port:     fp.GetPort(),
			family:   fp.GetFamily(),
			socatPid: pid,
			state:    PortStateForward,
		}
		go reapAdoptedSocat(pid)
		readopted++
	}

	return readopted
}

func familyToIPVersion(family uint32) uint32 {
	switch family {
	case syscall.AF_INET:
		return 4
	case syscall.AF_INET6:
		return 6
	default:
		return 0 // Unknown or unsupported family
	}
}
