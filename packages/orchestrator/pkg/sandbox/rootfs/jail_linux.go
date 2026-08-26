//go:build linux

package rootfs

import (
	"fmt"
	"time"
)

// jailProperties returns the systemd-run confinement properties shared by every
// jailed tool that parses tenant-controlled filesystem bytes on the host
// (debugfs for the envd swap, e2fsck for pre-boot recovery). It is the single
// source of the jail's security posture so the two callers cannot drift: empty
// root, DynamicUser in group disk, no network, minimal syscall surface, device
// access pinned to exactly one NBD node (rw). The caller appends `--`, its
// binary, and any tool-specific args; extraBinds adds tool-specific read-write
// binds (e.g. the swap's staging directory).
//
// The device path is assumed already validated against nbdDevicePath by the
// caller — the property list embeds it verbatim.
func jailProperties(unit string, runtimeMax time.Duration, devicePath string, extraBinds ...string) []string {
	args := []string{
		"--wait", "--pipe", "--collect", "--quiet",
		"--unit=" + unit,
		fmt.Sprintf("--property=RuntimeMaxSec=%d", int(runtimeMax.Seconds())),
		"--property=KillSignal=SIGKILL",
		"--property=TimeoutStopSec=10s",
		"--property=DynamicUser=yes",
		"--property=SupplementaryGroups=disk",
		"--property=ProtectProc=invisible",
		"--property=ProcSubset=pid",
		"--property=PrivateNetwork=yes",
		"--property=PrivateIPC=yes",
		"--property=ProtectHome=yes",
		"--property=NoNewPrivileges=yes",
		"--property=CapabilityBoundingSet=",
		"--property=AmbientCapabilities=",
		"--property=RestrictNamespaces=yes",
		"--property=SystemCallFilter=@system-service",
		// The tool rewrites structures inside the image via libext2fs, not via
		// host chown/setuid or resource syscalls, so subtract those from the
		// @system-service allow-list rather than leave them reachable.
		"--property=SystemCallFilter=~@privileged @resources",
		"--property=SystemCallArchitectures=native",
		"--property=RestrictAddressFamilies=AF_UNIX",
		"--property=LockPersonality=yes",
		"--property=ProtectClock=yes",
		"--property=ProtectKernelTunables=yes",
		"--property=ProtectKernelModules=yes",
		"--property=ProtectKernelLogs=yes",
		"--property=ProtectControlGroups=yes",
		"--property=ProtectHostname=yes",
		"--property=RestrictRealtime=yes",
		// The tool does not JIT; deny writable+executable mappings.
		"--property=MemoryDenyWriteExecute=yes",
		// PrivateNetwork already isolates the network; make the intent explicit.
		"--property=IPAddressDeny=any",
		"--property=Environment=",
		// Empty root: loader, libraries, and the target device are all that's
		// visible, plus any tool-specific bind below.
		"--property=TemporaryFileSystem=/",
		"--property=BindReadOnlyPaths=/usr",
		"--property=BindReadOnlyPaths=/usr/lib64:/lib64",
		"--property=BindReadOnlyPaths=/usr/lib:/lib",
		"--property=BindReadOnlyPaths=/usr/bin:/bin",
		"--property=BindReadOnlyPaths=/usr/sbin:/sbin",
		"--property=MountAPIVFS=yes",
		"--property=PrivateDevices=yes",
		fmt.Sprintf("--property=DeviceAllow=%s rw", devicePath),
		"--property=BindPaths=" + devicePath,
	}
	for _, b := range extraBinds {
		args = append(args, "--property=BindPaths="+b)
	}

	return args
}
