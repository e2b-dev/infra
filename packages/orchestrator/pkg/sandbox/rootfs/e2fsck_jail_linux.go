//go:build linux

package rootfs

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"time"
)

// maxE2fsckOutput caps how much of e2fsck's combined stdout/stderr we buffer. The
// output is tenant-influenced — a heavily corrupted image can make e2fsck -y emit
// very large diagnostics — and we only use it for a debug log, so bound it to keep
// one recovery from creating node-level memory pressure.
const maxE2fsckOutput = 64 << 10 // 64 KiB

// runE2fsckSandboxed runs e2fsck on a tenant-controlled ext4 filesystem under a
// transient, tightly-confined systemd-run service. e2fsprogs is a large parser of
// attacker-shapeable on-disk structures (journal, htree/extent trees, xattrs, ...)
// with a history of memory-safety bugs, so a crafted image could otherwise turn a
// parser bug into a tenant->host escape past the microVM isolation boundary.
//
// The confinement:
//   - DynamicUser — e2fsck runs as a transient non-root user (in group "disk" so
//     it can still open the root:disk nbd device), so it can neither read
//     root-owned host state nor regain root;
//   - ProtectProc=invisible + ProcSubset=pid — /proc shows only the unit's own
//     processes, so a compromise can't read other processes' cmdline/environ
//     (systemd mounts /proc for the hardening options and, lacking a private PID
//     namespace on systemd 255, it would otherwise expose host processes);
//   - PrivateNetwork — no network, so a compromised e2fsck can't exfiltrate or
//     move laterally (the writable rootfs it repairs is the only egress, and the
//     tenant re-mounts that after resume);
//   - TemporaryFileSystem=/ with only /usr (+ the usrmerge loader dirs) bound in
//     read-only and the target device bound read-write — no host files are
//     visible, so there are no host secrets to read;
//   - Environment= — a clean environment (the orchestrator's holds secrets);
//   - CapabilityBoundingSet=/AmbientCapabilities= empty + NoNewPrivileges;
//   - RestrictNamespaces — denies creating a new (user) namespace, closing the
//     capability-regain path, while still allowing e2fsck's bitmap worker thread;
//   - a @system-service seccomp allowlist.
//
// Recovery is best-effort at the call site, so if systemd-run is unavailable or
// the unit is killed the reboot falls back to the guest kernel's own jbd2 replay.
func runE2fsckSandboxed(ctx context.Context, devicePath string) ([]byte, error) {
	if !nbdDevicePath.MatchString(devicePath) {
		return nil, fmt.Errorf("refusing to run e2fsck on unexpected device path %q", devicePath)
	}

	// Deterministic per-device unit name so the transient unit can be force-stopped
	// on return (an nbd device backs at most one recovery at a time).
	unit := "e2fsck-recovery-" + filepath.Base(devicePath)

	args := []string{
		"--wait", "--pipe", "--collect", "--quiet",
		"--unit=" + unit,
		// systemd-run is only a D-Bus client; the unit runs under PID 1. Cap the run
		// server-side so the timeout is enforced even if the client dies, and kill
		// the whole cgroup on expiry.
		fmt.Sprintf("--property=RuntimeMaxSec=%d", int(JournalRecoveryTimeout.Seconds())),
		// Stop = an immediate SIGKILL of the whole cgroup, bounded: e2fsck holds no
		// state that a graceful SIGTERM would flush, and this guarantees the unit
		// (and e2fsck) is gone quickly on RuntimeMaxSec expiry or the explicit stop
		// below — so it can never outlive this call and write concurrently with the
		// export/boot that follows.
		"--property=KillSignal=SIGKILL",
		"--property=TimeoutStopSec=10s",
		// Run non-root so host root state is unreadable; "disk" grants access to the
		// root:disk nbd device.
		"--property=DynamicUser=yes",
		"--property=SupplementaryGroups=disk",
		// Hide every other process from /proc (systemd mounts /proc for the hardening
		// options below, and without a private PID namespace it would show host PIDs).
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
		"--property=SystemCallArchitectures=native",
		"--property=RestrictAddressFamilies=AF_UNIX",
		"--property=LockPersonality=yes",
		"--property=ProtectClock=yes",
		"--property=ProtectKernelTunables=yes",
		"--property=ProtectKernelModules=yes",
		"--property=Environment=",
		// e2fsck exit 1/2/3 (errors corrected, possibly reboot-recommended; the
		// combined bit is 3) mean the filesystem was made consistent — success, not
		// a unit failure. Any other non-zero exit stays a failure.
		"--property=SuccessExitStatus=1 2 3",
		// Empty root: only the loader, libraries, and the target device are visible.
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
		"--", "/usr/sbin/e2fsck", "-y", devicePath,
	}

	// Capture combined stdout/stderr into a size-capped buffer. Pointing Stdout and
	// Stderr at the same writer makes os/exec serialize them through one pipe, so
	// no locking is needed.
	out := &cappedBuffer{limit: maxE2fsckOutput}
	cmd := exec.CommandContext(ctx, "systemd-run", args...)
	cmd.Stdout = out
	cmd.Stderr = out
	err := cmd.Run()

	// systemd-run only supervises the client; the unit runs under PID 1. If the
	// client was killed (ctx deadline) while e2fsck was still running, e2fsck would
	// keep writing to the device concurrently with the export/boot that follows.
	// Force the unit (and its cgroup) down before returning so it can't outlive this
	// call; a detached context keeps this working even after ctx has expired. With
	// KillSignal=SIGKILL the stop is near-instant, and `systemctl stop` blocks until
	// the cgroup is empty, so on return e2fsck is guaranteed gone. The generous
	// timeout only bounds a wedged teardown; it is a no-op once the unit has already
	// exited and been collected.
	stopCtx, stopCancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer stopCancel()
	_ = exec.CommandContext(stopCtx, "systemctl", "stop", "--quiet", unit+".service").Run()

	return out.Bytes(), err
}

// cappedBuffer accumulates writes up to limit bytes and silently discards the
// rest, while always reporting a full write so the child process is never blocked
// or handed a short-write error.
type cappedBuffer struct {
	limit int
	buf   bytes.Buffer
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	if rem := c.limit - c.buf.Len(); rem > 0 {
		if len(p) > rem {
			c.buf.Write(p[:rem])
		} else {
			c.buf.Write(p)
		}
	}

	return len(p), nil
}

func (c *cappedBuffer) Bytes() []byte { return c.buf.Bytes() }

var nbdDevicePath = regexp.MustCompile(`^/dev/nbd[0-9]+$`)
