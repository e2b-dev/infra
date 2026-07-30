//go:build linux

package rootfs

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// EnvdSwapTimeout bounds a single debugfs invocation. It is paired with a
// cancel-free context so a request cancellation can't kill debugfs mid-write and
// leave a half-written inode on the device that the following cold boot (or a
// later pause export) would serve.
const EnvdSwapTimeout = 2 * time.Minute

// maxSwapOutput caps captured debugfs stdio so a chatty/looping tool can't grow
// memory without bound.
const maxSwapOutput = 64 << 10 // 64 KiB

// guestEnvdPath is the in-rootfs path the swap targets.
const guestEnvdPath = "/usr/bin/envd"

// nbdDevicePath constrains the device the jailed debugfs may touch to an NBD
// node, so a bad caller can't point it at an arbitrary block device.
var nbdDevicePath = regexp.MustCompile(`^/dev/nbd[0-9]+$`)

// ErrOfflineSwapUnrecoverable marks a swap that failed AND could not roll the
// original back, so the rootfs may have no usable /usr/bin/envd. The caller must
// NOT boot such a rootfs (a running-but-envd-less sandbox is useless) — it should
// fail the operation so the dirty overlay is discarded. Every other swap failure
// leaves the original envd in place (untouched or restored) and is recoverable.
var ErrOfflineSwapUnrecoverable = errors.New("offline envd swap failed and the original was not restored")

// SwapEnvdBinary replaces /usr/bin/envd inside an unmounted ext4 rootfs device
// with the binary at srcPath, entirely in userspace via debugfs (libext2fs) —
// never a host-kernel mount of the tenant image. Intended to run in the reboot
// PreBootFn, before Firecracker boots, so a filesystem-only snapshot cold-boots
// onto a newer envd. The device must not be mounted or written concurrently.
//
// The swap is transactional against the "never brick a sandbox" guarantee:
// debugfs runs a script of commands and exits 0 even if an individual command
// (rm/write/sif) fails, so a naive rm-then-write could delete the old envd, fail
// the write (e.g. ENOSPC), and still exit 0 — leaving the rootfs with no envd
// while reporting success. To avoid that, this:
//
//  1. backs the original binary out to the host (so there is always something to
//     roll back to), refusing to proceed if it can't;
//  2. performs the rm+write+sif swap;
//  3. verifies by reading /usr/bin/envd back out and comparing its content hash to
//     the intended binary — the process exit is not trusted;
//  4. on any mismatch, restores the original from the host backup and returns an
//     error, so the guest boots its original envd rather than a broken rootfs.
func SwapEnvdBinary(ctx context.Context, devicePath, srcPath string) (e error) {
	ctx, span := tracer.Start(ctx, "envd-binary-swap", trace.WithAttributes(
		attribute.String("device", devicePath),
		attribute.String("src", srcPath),
	))
	defer span.End()

	// Stage the new binary and the debugfs command/dump files in a private
	// directory bound into the jail. The target's real home (/fc-envd) is a
	// gcsfuse mount that need not propagate into the unit's private mount
	// namespace, so copy it onto local disk first.
	stage, err := os.MkdirTemp("", "envd-swap-*")
	if err != nil {
		return fmt.Errorf("create swap stage dir: %w", err)
	}
	defer os.RemoveAll(stage)

	// The jail runs debugfs as a transient DynamicUser that must traverse this
	// directory to read the staged binary/scripts and write its dumps; MkdirTemp
	// creates it 0700 (owner-only), so widen it to 0755.
	if err := os.Chmod(stage, 0o755); err != nil {
		return fmt.Errorf("chmod swap stage dir: %w", err)
	}

	stagedNew := filepath.Join(stage, "envd.new")
	if err := copyFile(srcPath, stagedNew, 0o755); err != nil {
		return fmt.Errorf("stage target envd %q: %w", srcPath, err)
	}
	// The jailed debugfs runs as an unprivileged DynamicUser with no
	// CAP_DAC_OVERRIDE, so it can only read the staged binary via its world bits.
	// copyFile's mode is subject to the orchestrator umask, so set it explicitly.
	if err := os.Chmod(stagedNew, 0o755); err != nil {
		return fmt.Errorf("chmod staged envd: %w", err)
	}
	wantSHA, err := fileSHA256(stagedNew)
	if err != nil {
		return fmt.Errorf("hash target envd: %w", err)
	}

	// 1. Back the original out to the host BEFORE touching it, so any later
	// failure can be rolled back. debugfs `dump` reads the inode to a host file.
	// Pre-create the target world-writable: the DynamicUser can't create files in
	// the root-owned stage dir, but can write an already-existing 0666 file.
	origPath := filepath.Join(stage, "envd.orig")
	if err := createJailWritable(origPath); err != nil {
		return fmt.Errorf("pre-create backup target: %w", err)
	}
	if out, derr := runDebugfs(ctx, devicePath, stage, "backup",
		fmt.Sprintf("dump %s %s\n", guestEnvdPath, origPath), false); derr != nil {
		return fmt.Errorf("back up original envd: %w (output: %q)", derr, string(out))
	}
	if fi, serr := os.Stat(origPath); serr != nil || fi.Size() == 0 {
		// No original to fall back to — refuse rather than risk an unrecoverable
		// swap. (A well-formed rootfs always has /usr/bin/envd.)
		return fmt.Errorf("refusing offline swap: could not read original %s to back up (dump produced nothing)", guestEnvdPath)
	}
	// createJailWritable left the backup 0666 so the jail could write the dump; the
	// rollback later `write`s it back, so make it executable-moded now (belt-and-
	// suspenders — the rollback also `sif`s it and verifyEnvd asserts the result).
	if err := os.Chmod(origPath, 0o755); err != nil {
		return fmt.Errorf("chmod original backup: %w", err)
	}
	origSHA, err := fileSHA256(origPath)
	if err != nil {
		return fmt.Errorf("hash original envd: %w", err)
	}

	// 2. Swap: remove the old inode and write the new one. Do NOT branch on the
	// process exit — debugfs exits 0 even on per-command failures, and a non-zero
	// exit (timeout, kill, jail/device failure) says nothing reliable about the
	// on-disk state. The decision below reads the actual rootfs and acts on that.
	swapScript := fmt.Sprintf("rm %s\nwrite %s %s\nsif %s mode 0100755\n",
		guestEnvdPath, stagedNew, guestEnvdPath, guestEnvdPath)
	swapOut, swapErr := runDebugfs(ctx, devicePath, stage, "swap", swapScript, true)
	swapCtx := "debugfs exited cleanly"
	if swapErr != nil {
		swapCtx = fmt.Sprintf("debugfs errored: %v (output %q)", swapErr, string(swapOut))
	}

	// 3. Decide from the ACTUAL rootfs state, not the exit code.
	if verifyEnvd(ctx, devicePath, stage, "verify", wantSHA) == nil {
		return nil // /usr/bin/envd is the new binary and executable — swap landed
	}

	// Not the new binary. Read what is actually on disk to choose the safe action.
	gotSHA, readErr := dumpSHA256(ctx, devicePath, stage, "state")
	switch {
	case readErr != nil:
		// Can't even read the rootfs (e.g. a persistent jail/device failure). We
		// cannot confirm a bootable envd and must not rm/boot blindly, so fail the
		// boot to discard the dirty overlay rather than risk a broken sandbox.
		return fmt.Errorf("%w: swap did not land and rootfs state is unreadable (%s): %w",
			ErrOfflineSwapUnrecoverable, swapCtx, readErr)
	case gotSHA == origSHA:
		// The original is untouched (the swap never modified it — e.g. the jail
		// failed to start). Boot it: no destructive rollback needed. Recoverable.
		return fmt.Errorf("offline envd swap did not apply; original left in place (%s)", swapCtx)
	default:
		// envd is damaged or gone (partial write, or removed then not rewritten —
		// e.g. a kill after `rm`). Restore the original from the host backup.
		if rbErr := rollbackEnvd(ctx, devicePath, stage, origPath); rbErr != nil {
			return fmt.Errorf("%w: swap left envd damaged and rollback failed (%s): %w",
				ErrOfflineSwapUnrecoverable, swapCtx, rbErr)
		}

		return fmt.Errorf("offline envd swap did not land (envd was damaged, %s); original envd restored", swapCtx)
	}
}

// rollbackEnvd restores the original binary from the host backup at origPath.
// Best-effort within the "never brick" guarantee: it rewrites the original and
// then reads it back to confirm the restore actually landed.
func rollbackEnvd(ctx context.Context, devicePath, stageDir, origPath string) error {
	// The rollback is the safety net, so it must not inherit the (possibly already
	// expired) deadline of the swap it is recovering from: detach from cancellation
	// and give it its own budget, or a timeout mid-swap would leave envd deleted.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), EnvdSwapTimeout)
	defer cancel()

	script := fmt.Sprintf("rm %s\nwrite %s %s\nsif %s mode 0100755\n",
		guestEnvdPath, origPath, guestEnvdPath, guestEnvdPath)
	if out, err := runDebugfs(ctx, devicePath, stageDir, "rollback", script, true); err != nil {
		return fmt.Errorf("restore original envd: %w (output: %q)", err, string(out))
	}

	wantSHA, err := fileSHA256(origPath)
	if err != nil {
		return fmt.Errorf("hash original envd for rollback check: %w", err)
	}
	// Verify the restore landed as an executable regular file — same content+mode
	// check as the forward swap, so a silent `sif` failure during rollback can't
	// leave a non-executable envd that passes a content-only check.
	if err := verifyEnvd(ctx, devicePath, stageDir, "rollback-verify", wantSHA); err != nil {
		return fmt.Errorf("verify restored envd: %w", err)
	}

	return nil
}

// verifyEnvd confirms /usr/bin/envd in the rootfs matches wantSHA in content AND
// is an executable regular file. debugfs exits 0 even when a scripted command
// (write/sif) failed, so both are read back from the device — a content match
// alone would miss a silent sif failure that left the binary non-executable.
func verifyEnvd(ctx context.Context, devicePath, stageDir, phase, wantSHA string) error {
	gotSHA, err := dumpSHA256(ctx, devicePath, stageDir, phase)
	if err != nil {
		return fmt.Errorf("read content: %w", err)
	}
	if gotSHA != wantSHA {
		return fmt.Errorf("content mismatch: got %q want %q", gotSHA, wantSHA)
	}

	// debugfs `stat` prints the inode header (Type/Mode) to stdout — no output file.
	out, err := runDebugfs(ctx, devicePath, stageDir, phase+"-stat",
		fmt.Sprintf("stat %s\n", guestEnvdPath), false)
	if err != nil {
		return fmt.Errorf("stat for mode check: %w (output: %q)", err, string(out))
	}
	if !envdExecutable(string(out)) {
		return fmt.Errorf("%s is not an executable regular file after write", guestEnvdPath)
	}

	return nil
}

// debugfsModeRe extracts the octal permission bits from a debugfs `stat` header
// line, e.g. "Inode: 12   Type: regular    Mode:  0755   Flags: 0x0".
var debugfsModeRe = regexp.MustCompile(`Mode:\s*0*([0-7]{3,4})`)

// envdExecutable reports whether debugfs `stat` output describes a regular file
// with the owner-execute bit set.
func envdExecutable(statOut string) bool {
	if !strings.Contains(statOut, "Type: regular") {
		return false
	}
	m := debugfsModeRe.FindStringSubmatch(statOut)
	if m == nil {
		return false
	}
	perm, err := strconv.ParseInt(m[1], 8, 32)
	if err != nil {
		return false
	}

	return perm&0o100 != 0
}

// dumpSHA256 reads /usr/bin/envd out of the rootfs (read-only) and returns the
// sha256 of its bytes.
func dumpSHA256(ctx context.Context, devicePath, stageDir, phase string) (string, error) {
	out := filepath.Join(stageDir, "envd."+phase)
	// Pre-create world-writable so the jailed DynamicUser can write the dump into
	// the root-owned stage dir (see createJailWritable).
	if err := createJailWritable(out); err != nil {
		return "", fmt.Errorf("pre-create dump target: %w", err)
	}
	if o, err := runDebugfs(ctx, devicePath, stageDir, phase,
		fmt.Sprintf("dump %s %s\n", guestEnvdPath, out), false); err != nil {
		return "", fmt.Errorf("dump %s: %w (output: %q)", guestEnvdPath, err, string(o))
	}

	return fileSHA256(out)
}

// createJailWritable creates (or truncates) path as an empty world-writable file
// so the jailed debugfs DynamicUser can open it as a `dump` target: it cannot
// create files in the root-owned 0755 stage dir, but opening an already-existing
// 0666 file for writing needs no directory write. The explicit chmod defeats the
// process umask.
func createJailWritable(path string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o666)
	if err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}

	return os.Chmod(path, 0o666)
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()

		return err
	}

	return out.Close()
}

// runDebugfs runs `debugfs [-w] -f <script> <device>` under a systemd-run seccomp
// jail: empty root, DynamicUser in group disk, no network, minimal syscall
// surface — so a crash while parsing the tenant filesystem is contained, not a
// host compromise. writable adds -w (the device is opened read-only otherwise, so
// a dump/verify cannot mutate the tenant image). The staged directory (new binary,
// command file, and dump outputs) is bind-mounted read-write at its host path so
// `write`/`-f` sources and `dump` targets resolve unchanged. phase names the
// transient unit and script file so sequential invocations don't collide.
func runDebugfs(ctx context.Context, devicePath, stageDir, phase, script string, writable bool) ([]byte, error) {
	if !nbdDevicePath.MatchString(devicePath) {
		return nil, fmt.Errorf("refusing to run debugfs on unexpected device path %q", devicePath)
	}

	scriptPath := filepath.Join(stageDir, "cmds-"+phase)
	if err := os.WriteFile(scriptPath, []byte(script), 0o644); err != nil {
		return nil, fmt.Errorf("write debugfs script: %w", err)
	}
	// WriteFile's mode is subject to umask; the jailed DynamicUser reads the script
	// via its world bits, so set it explicitly.
	if err := os.Chmod(scriptPath, 0o644); err != nil {
		return nil, fmt.Errorf("chmod debugfs script: %w", err)
	}

	unit := "envd-swap-" + phase + "-" + filepath.Base(devicePath)

	args := []string{
		"--wait", "--pipe", "--collect", "--quiet",
		"--unit=" + unit,
		fmt.Sprintf("--property=RuntimeMaxSec=%d", int(EnvdSwapTimeout.Seconds())),
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
		// debugfs rewrites inode fields inside the image via libext2fs, not via
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
		// debugfs does not JIT; deny writable+executable mappings.
		"--property=MemoryDenyWriteExecute=yes",
		// PrivateNetwork already isolates the network; make the intent explicit.
		"--property=IPAddressDeny=any",
		"--property=Environment=",
		// Empty root: loader, libraries, target device, and the staged directory
		// (read-write, for dump outputs) are all that's visible.
		"--property=TemporaryFileSystem=/",
		"--property=BindReadOnlyPaths=/usr",
		"--property=BindReadOnlyPaths=/usr/lib64:/lib64",
		"--property=BindReadOnlyPaths=/usr/lib:/lib",
		"--property=BindReadOnlyPaths=/usr/bin:/bin",
		"--property=BindReadOnlyPaths=/usr/sbin:/sbin",
		"--property=BindPaths=" + stageDir,
		"--property=MountAPIVFS=yes",
		"--property=PrivateDevices=yes",
		fmt.Sprintf("--property=DeviceAllow=%s rw", devicePath),
		"--property=BindPaths=" + devicePath,
		"--",
		"/usr/sbin/debugfs",
	}
	if writable {
		args = append(args, "-w")
	}
	args = append(args, "-f", scriptPath, devicePath)

	out := &cappedBuffer{limit: maxSwapOutput}
	cmd := exec.CommandContext(ctx, "systemd-run", args...)
	cmd.Stdout = out
	cmd.Stderr = out
	err := cmd.Run()

	// Force the transient unit down before returning so debugfs can never outlive
	// this call and write concurrently with the boot/export that follows.
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
