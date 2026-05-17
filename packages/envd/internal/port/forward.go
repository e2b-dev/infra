// portf (port forward) periodaically scans opened TCP ports on the 127.0.0.1 (or localhost)
// and launches `socat` process for every such port in the background.
// socat forward traffic from `sourceIP`:port to the 127.0.0.1:port.

// WARNING: portf isn't thread safe!

package port

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"syscall"

	"github.com/rs/zerolog"

	"github.com/e2b-dev/infra/packages/envd/internal/services/cgroups"
)

// envPortForwarderIPv4Iptables, when "1"/"true", switches IPv4 port
// forwarding from per-port socat to iptables DNAT (IPv6 keeps socat).
const envPortForwarderIPv4Iptables = "ENVD_PORT_FORWARDER_IPV4_IPTABLES"

func iptablesIPv4Enabled() bool {
	v := os.Getenv(envPortForwarderIPv4Iptables)
	return v == "1" || v == "true"
}

type PortState string

const (
	PortStateForward PortState = "FORWARD"
	PortStateDelete  PortState = "DELETE"
)

var defaultGatewayIP = net.IPv4(169, 254, 0, 21)

type PortToForward struct {
	socat       *exec.Cmd
	family      uint32
	state       PortState
	port        uint32
	missedScans int
	// iptables is true when this port is forwarded via an iptables DNAT rule
	// instead of a socat process. Only set for IPv4 listeners when the
	// PortForwarderIPv4UseIptables flag is on.
	iptables bool
}

// portDeleteThreshold is the number of consecutive scans without seeing the
// listener before stopping its socat (~3 s at 1 Hz). Absorbs HMR/restart flicker.
const portDeleteThreshold = 3

type Forwarder struct {
	logger        *zerolog.Logger
	cgroupManager cgroups.Manager
	// Map of ports that are being currently forwarded.
	ports             map[string]*PortToForward
	scannerSubscriber *ScannerSubscriber
	sourceIP          net.IP
	iptables          *iptablesBackend
	useIptables       bool
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
		iptables:          newIPtablesBackend(defaultGatewayIP.String()),
		useIptables:       iptablesIPv4Enabled(),
	}
}

func (f *Forwarder) StartForwarding(ctx context.Context) {
	if f.scannerSubscriber == nil {
		f.logger.Error().Msg("Cannot start forwarding because scanner subscriber is nil")

		return
	}

	if f.useIptables {
		if err := setupIPv4DNAT(); err != nil {
			f.logger.Warn().Err(err).Msg("Failed to enable route_localnet; iptables IPv4 forwarding may fail")
		}
	}

	for {
		// procs is an array of currently opened ports.
		procs, ok := <-f.scannerSubscriber.Messages
		if !ok {
			return
		}

		for _, v := range f.ports {
			v.state = PortStateDelete
		}

		for _, p := range procs {
			// Key on port only so a dual-stack listener (127.0.0.1 + ::1 same
			// port) maps to a single socat — socat always binds the v4 gateway
			// regardless of family, so two entries would collide on bind.
			key := fmt.Sprintf("%d", p.Laddr.Port)

			if val, portOk := f.ports[key]; portOk {
				val.state = PortStateForward
				val.missedScans = 0

				continue
			}

			f.logger.Debug().
				Str("ip", p.Laddr.IP).
				Uint32("port", p.Laddr.Port).
				Uint32("family", familyToIPVersion(p.Family)).
				Str("state", p.Status).
				Msg("Detected new opened port on localhost that is not forwarded")

			ptf := &PortToForward{
				port:   p.Laddr.Port,
				state:  PortStateForward,
				family: familyToIPVersion(p.Family),
			}
			f.ports[key] = ptf
			f.startPortForwarding(ctx, ptf)
		}

		for key, v := range f.ports {
			if v.state != PortStateDelete {
				continue
			}
			v.missedScans++
			if v.missedScans >= portDeleteThreshold {
				f.stopPortForwarding(v)
				delete(f.ports, key)
			}
		}
	}
}

func (f *Forwarder) startPortForwarding(ctx context.Context, p *PortToForward) {
	if p.family == 4 && f.useIptables {
		if err := f.iptables.addRule(ctx, p.port); err != nil {
			f.logger.Error().Err(err).Uint32("port", p.port).Msg("iptables DNAT add failed; falling back to socat")
		} else {
			p.iptables = true
			f.logger.Debug().Uint32("port", p.port).Msg("Started IPv4 port forwarding via iptables DNAT")
			return
		}
	}

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
	if p.iptables {
		if err := f.iptables.deleteRule(context.Background(), p.port); err != nil {
			f.logger.Error().Err(err).Uint32("port", p.port).Msg("iptables DNAT delete failed")
		}
		p.iptables = false
		return
	}

	if p.socat == nil {
		return
	}

	defer func() { p.socat = nil }()

	logger := f.logger.With().
		Str("socatCmd", p.socat.String()).
		Uint32("family", p.family).
		IPAddr("sourceIP", f.sourceIP.To4()).
		Uint32("port", p.port).
		Logger()

	logger.Debug().Msg("Stopping port forwarding")

	if err := syscall.Kill(-p.socat.Process.Pid, syscall.SIGKILL); err != nil {
		logger.Error().Err(err).Msg("Failed to kill process group")

		return
	}

	logger.Debug().Msg("Stopped port forwarding")
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
