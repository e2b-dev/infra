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

// EnvdSwapTimeout bounds a SINGLE debugfs invocation — it is what RuntimeMaxSec is
// set to. It is paired with a cancel-free context so a request cancellation can't
// kill debugfs mid-write and leave a half-written inode on the device that the
// following cold boot (or a later pause export) would serve.
const EnvdSwapTimeout = 2 * time.Minute

// EnvdSwapBudget bounds the mutating phases of a whole SwapEnvdBinary call (the
// backup dump and the swap) and is what callers should wrap the call in. It is a
// MULTIPLE of EnvdSwapTimeout rather than equal to it: those are two debugfs
// invocations, each separately entitled to EnvdSwapTimeout, so budgeting the whole
// call at one invocation's bound lets a slow backup — a cold NBD read of the whole
// binary, chunk-fetched from object storage — burn the clock the swap still needs.
// The read-back phases that decide the outcome (verify, state read, rollback) are
// detached from this budget and get their own, so a spent budget can never leave
// the rootfs state unknowable and turn a recoverable swap into a failed boot.
const EnvdSwapBudget = 2 * EnvdSwapTimeout

// maxSwapOutput caps captured debugfs stdio so a chatty/looping tool can't grow
// memory without bound.
const maxSwapOutput = 64 << 10 // 64 KiB

// maxEnvdSize bounds the rootfs /usr/bin/envd the swap will touch. Unlike the
// binary's CONTENT — guest-controlled but harmless, since it only ever goes back
// into that guest's own rootfs — its SIZE is guest-controlled AND lands on the
// host: every phase dumps the inode to the local temp filesystem (backup, verify,
// state, rollback-verify), so up to four copies coexist for the length of a swap.
// Unbounded, one sandbox could point this at a multi-gigabyte file and fill the
// host, taking out every other sandbox on the node. Real envd binaries measure
// 13-19 MiB, so this leaves better than an order of magnitude of headroom while
// bounding one swap's host footprint. Checked once, before the backup: afterwards
// the inode is either our own staged binary or an original already under the bound,
// so the later dumps need no separate check.
const maxEnvdSize = 256 << 20 // 256 MiB

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

// ErrEnvdTooLarge marks a rootfs refused before anything was touched because its
// /usr/bin/envd exceeds maxEnvdSize. Recoverable by construction — nothing was
// mutated, so the guest boots its own envd — but worth its own sentinel so a ramp
// can tell "we declined this rootfs" from "the swap broke".
var ErrEnvdTooLarge = errors.New("rootfs envd is too large to swap offline")

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
//  1. refuses outright if the rootfs envd is over maxEnvdSize, before anything is
//     read or written — its size is guest-controlled and every later dump lands on
//     the host;
//  2. backs the original binary out to the host (so there is always something to
//     roll back to), refusing to proceed if it can't;
//  3. performs the rm+write+sif swap;
//  4. reads /usr/bin/envd back ONCE — inode via `stat`, bytes via `dump` — and
//     classifies it through classifyEnvdState; the process exit is not trusted, and
//     there is no second opinion to disagree with;
//  5. acts on that single verdict: success, boot-the-original, restore-from-backup,
//     or (only when the state is genuinely indeterminate) refuse to boot at all.
//
// ctx bounds only steps 1-3 (the size check and the mutating phases); callers should
// give it EnvdSwapBudget. Steps 4-5 decide the outcome and run on their own budgets,
// so a spent ctx cannot make the rootfs state unknowable and fail an otherwise fine
// boot.
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

	return swapEnvd(ctx, swapIO{
		stageDir: stage,
		run: func(ctx context.Context, phase, script string, writable bool) ([]byte, error) {
			return runDebugfs(ctx, devicePath, stage, phase, script, writable)
		},
	}, srcPath)
}

// swapIO is everything the swap needs from the outside world: a staging directory
// and one way to run a debugfs script against the device. Production binds it to
// the jailed runner in SwapEnvdBinary; tests substitute a fake, which is what makes
// the decision flow — not just the classification table — testable without debugfs,
// NBD, systemd or privileges. The device-path allowlist lives in runDebugfs and so
// is exercised by its own tests, not bypassed by this seam.
type swapIO struct {
	stageDir string
	run      func(ctx context.Context, phase, script string, writable bool) ([]byte, error)
}

// swapEnvd is SwapEnvdBinary's body, minus the staging directory's lifecycle. See
// SwapEnvdBinary for the contract; the numbered steps below match its doc comment.
func swapEnvd(ctx context.Context, dbg swapIO, srcPath string) error {
	stage := dbg.stageDir
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
	// An empty staged binary would make wantSHA the empty-file digest, which a failed
	// (empty) read-back would then MATCH — the one way a broken swap could report
	// success. dumpSHA256 rejects empty reads; refuse an empty source to close the pair.
	if fi, serr := os.Stat(stagedNew); serr != nil || fi.Size() == 0 {
		return fmt.Errorf("refusing offline swap: staged target envd %q is empty or unreadable", srcPath)
	}
	wantSHA, err := fileSHA256(stagedNew)
	if err != nil {
		return fmt.Errorf("hash target envd: %w", err)
	}

	// 1. Refuse an implausibly large rootfs envd BEFORE dumping it: the size is
	// guest-controlled and each dump lands on the host's temp filesystem, so an
	// unbounded inode is a lever for one sandbox to fill the node (see maxEnvdSize).
	// `stat` reads the size from the inode without transferring its contents.
	origSize, err := statEnvdSize(ctx, dbg)
	if err != nil {
		return fmt.Errorf("size-check original envd: %w", err)
	}
	if origSize > maxEnvdSize {
		return fmt.Errorf("%w: %s is %d bytes, over the %d-byte limit",
			ErrEnvdTooLarge, guestEnvdPath, origSize, int64(maxEnvdSize))
	}

	// 2. Back the original out to the host BEFORE touching it, so any later
	// failure can be rolled back. debugfs `dump` reads the inode to a host file.
	// Pre-create the target world-writable: the DynamicUser can't create files in
	// the root-owned stage dir, but can write an already-existing 0666 file.
	origPath := filepath.Join(stage, "envd.orig")
	if err := createJailWritable(origPath); err != nil {
		return fmt.Errorf("pre-create backup target: %w", err)
	}
	if out, derr := dbg.run(ctx, "backup",
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

	// 3. Swap: remove the old inode and write the new one. Do NOT branch on the
	// process exit — debugfs exits 0 even on per-command failures, and a non-zero
	// exit (timeout, kill, jail/device failure) says nothing reliable about the
	// on-disk state. The decision below reads the actual rootfs and acts on that.
	swapScript := fmt.Sprintf("rm %s\nwrite %s %s\nsif %s mode 0100755\n",
		guestEnvdPath, stagedNew, guestEnvdPath, guestEnvdPath)
	swapOut, swapErr := dbg.run(ctx, "swap", swapScript, true)
	swapCtx := "debugfs exited cleanly"
	if swapErr != nil {
		swapCtx = fmt.Sprintf("debugfs errored: %v (output %q)", swapErr, string(swapOut))
	}

	// 4. Decide from the ACTUAL rootfs state, not the exit code — in ONE read. There
	// is deliberately no separate "verify" pass: two reads can disagree, and the
	// earlier design treated any disagreement as failure, so a single glitched read
	// drove the destructive rollback across a rootfs the swap had already fixed. The
	// read runs on its own budget (decisionCtx) because the caller's may be spent.
	stateCtx, cancelState := decisionCtx(ctx)
	state := readEnvdState(stateCtx, dbg, "state", wantSHA, origSHA)
	cancelState()

	switch classifyEnvdState(state.presence, state.content) {
	case envdSwapApplied:
		return nil // envd IS the new binary and executable — the swap landed

	case envdOriginalIntact:
		// The original is untouched and bootable (the swap never modified it — e.g.
		// the jail failed to start). Boot it: no destructive rollback. Recoverable.
		return fmt.Errorf("offline envd swap did not apply; original left in place (%s)", swapCtx)

	case envdDamaged:
		// envd is absent, or present but neither bootable nor recognisable — the
		// canonical never-brick case. Restore the original from the host backup.
		if rbErr := rollbackEnvd(ctx, dbg, origPath); rbErr != nil {
			return fmt.Errorf("%w: swap left envd damaged and rollback failed (%s): %w",
				ErrOfflineSwapUnrecoverable, swapCtx, rbErr)
		}

		return fmt.Errorf("offline envd swap did not land (envd was %s, %s); original envd restored",
			state.describe(), swapCtx)

	default: // envdUnknown
		// We could not establish what is on the rootfs at all. Not the same as
		// "damaged": rolling back means `rm` first, which on an unknown device could
		// destroy a perfectly good envd. Fail the boot so the overlay is discarded.
		return fmt.Errorf("%w: swap did not land and rootfs state is indeterminate (%s): %s",
			ErrOfflineSwapUnrecoverable, swapCtx, state.describe())
	}
}

// decisionCtx returns a cancel-free context with its own fresh EnvdSwapTimeout for
// a phase that decides the outcome: the read-backs and the rollback. They are the
// safety net, so they must not inherit a budget the earlier phases may already have
// spent — a rollback starved of clock leaves envd deleted, and a read-back starved
// of clock makes the rootfs state unknowable, which fails an otherwise fine boot.
func decisionCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), EnvdSwapTimeout)
}

// rollbackEnvd restores the original binary from the host backup at origPath.
// Best-effort within the "never brick" guarantee: it rewrites the original and
// then reads it back to confirm the restore actually landed.
func rollbackEnvd(ctx context.Context, dbg swapIO, origPath string) error {
	writeCtx, cancelWrite := decisionCtx(ctx)
	script := fmt.Sprintf("rm %s\nwrite %s %s\nsif %s mode 0100755\n",
		guestEnvdPath, origPath, guestEnvdPath, guestEnvdPath)
	// Do NOT branch on the process exit, for the same reason the forward swap does
	// not: debugfs exits 0 on per-command failures, and a non-zero exit — a kill, or
	// RuntimeMaxSec firing after the restore already landed — says nothing about
	// what is on disk. Only the read-back below decides, so a restore that succeeded
	// under a dying process is not reported as an unrecoverable failure.
	out, rbErr := dbg.run(writeCtx, "rollback", script, true)
	cancelWrite()

	origSHA, err := fileSHA256(origPath)
	if err != nil {
		return fmt.Errorf("hash original envd for rollback check: %w", err)
	}
	// Confirm through the same table as the forward swap: the restore has to leave the
	// original present, executable AND matching, so a silent `sif` failure during the
	// rollback cannot pass a content-only check. Its own budget again — the rewrite
	// above must not be able to starve the check that confirms it. Passing origSHA as
	// BOTH want and orig makes "applied" mean "the original is back".
	verifyCtx, cancelVerify := decisionCtx(ctx)
	st := readEnvdState(verifyCtx, dbg, "rollback-verify", origSHA, origSHA)
	cancelVerify()

	if classifyEnvdState(st.presence, st.content) != envdSwapApplied {
		if rbErr != nil {
			return fmt.Errorf("restored envd is %s (restore debugfs errored: %w, output %q)",
				st.describe(), rbErr, string(out))
		}

		return fmt.Errorf("restored envd is %s", st.describe())
	}

	return nil
}

// The post-swap decision is a table over the only two things a read-back can
// actually establish: what `debugfs stat` says about the inode (envdPresence) and
// what the dumped bytes are (envdContent). Both have an indeterminate value, and
// keeping those distinct from the negative ones is the whole point: three earlier
// versions of this logic each collapsed "we could not tell" into a definite answer
// and each broke a different case.

// envdPresence is what `debugfs stat` established about /usr/bin/envd.
type envdPresence int

const (
	// presenceUnknown: stat itself failed — nothing about the inode is established.
	presenceUnknown envdPresence = iota
	// presenceAbsent: stat says there is no such file. A KNOWN state, and the
	// canonical one this primitive exists for: `rm` landed and `write` did not.
	presenceAbsent
	// presentNotExecutable: the inode is there but is not an executable regular
	// file — a silent `sif` failure, or something that is not a plain file.
	presentNotExecutable
	// presentExecutable: an executable regular file, so whatever it holds can boot.
	presentExecutable
)

// envdContent is the dumped bytes, classified against what the swap knows.
type envdContent int

const (
	// contentUnreadable: the inode exists but its bytes could not be read (a dump
	// that failed, or one that silently produced nothing). Indeterminate.
	contentUnreadable envdContent = iota
	// contentTarget: the bytes are the binary the swap intended to install.
	contentTarget
	// contentOriginal: the bytes are the original the swap backed up.
	contentOriginal
	// contentOther: readable, but neither — a partial write, or a foreign binary.
	contentOther
)

// envdSwapState is the action the post-swap state calls for.
type envdSwapState int

const (
	envdSwapApplied    envdSwapState = iota // the new binary is in place and bootable
	envdOriginalIntact                      // the original is in place and bootable
	envdDamaged                             // known-bad: restore the original from the host backup
	envdUnknown                             // indeterminate: do not touch it, fail the boot
)

// classifyEnvdState is the decision table, total over both enums.
//
//	presence            content        -> action
//	unknown             (any)          -> unknown    (cannot stat: never rm, never boot)
//	absent              (n/a)          -> damaged    (envd is GONE: roll the original back)
//	present*            unreadable     -> unknown    (exists but unreadable: real device failure)
//	presentNotExecutable readable      -> damaged    (cannot exec whatever it is: rewrite it)
//	presentExecutable   target         -> applied
//	presentExecutable   original       -> originalIntact
//	presentExecutable   other          -> damaged    (partial write / foreign binary)
//
// Two distinctions carry the weight. **Absent is not unreadable**: a missing inode
// and a failed read both yield zero bytes, but the first is knowledge (roll back)
// and the second is ignorance (fail the boot) — conflating them turned the swap's
// central case into a failed resume. **Executable is checked independently of
// content**: on the idempotent re-fire the rootfs already carries the target, so
// wantSHA == origSHA and content alone cannot reveal a lost exec bit.
func classifyEnvdState(p envdPresence, c envdContent) envdSwapState {
	switch p {
	case presenceUnknown:
		return envdUnknown
	case presenceAbsent:
		return envdDamaged
	}

	if c == contentUnreadable {
		return envdUnknown
	}
	if p == presentNotExecutable {
		return envdDamaged
	}

	switch c {
	case contentTarget:
		return envdSwapApplied
	case contentOriginal:
		return envdOriginalIntact
	}

	return envdDamaged
}

// envdState is one read-back of /usr/bin/envd: the two classified observations,
// plus what was seen, for the error message.
type envdState struct {
	presence envdPresence
	content  envdContent
	sha      string // "" when unreadable
	statErr  error
	dumpErr  error
}

// describe renders the state for an operator reading a failure.
func (s envdState) describe() string {
	p := map[envdPresence]string{
		presenceUnknown:      "un-stat-able",
		presenceAbsent:       "absent",
		presentNotExecutable: "present but not an executable regular file",
		presentExecutable:    "present and executable",
	}[s.presence]
	c := map[envdContent]string{
		contentUnreadable: "unreadable",
		contentTarget:     "the target binary",
		contentOriginal:   "the original binary",
		contentOther:      "neither the target nor the original",
	}[s.content]

	out := fmt.Sprintf("%s, content %s", p, c)
	if s.sha != "" {
		out += " (sha " + s.sha[:min(12, len(s.sha))] + ")"
	}
	if s.statErr != nil {
		out += fmt.Sprintf("; stat: %v", s.statErr)
	}
	if s.dumpErr != nil {
		out += fmt.Sprintf("; dump: %v", s.dumpErr)
	}

	return out
}

// readEnvdState takes one look at /usr/bin/envd and classifies it. It never returns
// an error: a failure to read IS a state (presenceUnknown / contentUnreadable), and
// the caller must decide on it rather than treat it as an exception.
//
// stat comes first, and a definitive "not there" short-circuits the dump — there is
// nothing to read, and reading anyway is what previously made an absent binary look
// like a broken device.
func readEnvdState(ctx context.Context, dbg swapIO, phase, wantSHA, origSHA string) envdState {
	st := envdState{}

	out, err := dbg.run(ctx, phase+"-stat",
		fmt.Sprintf("stat %s\n", guestEnvdPath), false)
	switch {
	case err != nil:
		st.presence, st.statErr = presenceUnknown, fmt.Errorf("%w (output: %q)", err, string(out))

		return st
	case envdAbsent(string(out)):
		st.presence = presenceAbsent

		return st // nothing to dump
	case envdExecutable(string(out)):
		st.presence = presentExecutable
	default:
		st.presence = presentNotExecutable
	}

	gotSHA, derr := dumpSHA256(ctx, dbg, phase)
	if derr != nil {
		st.content, st.dumpErr = contentUnreadable, derr

		return st
	}
	st.sha = gotSHA
	switch gotSHA {
	case wantSHA:
		st.content = contentTarget
	case origSHA:
		st.content = contentOriginal
	default:
		st.content = contentOther
	}

	return st
}

// envdAbsent reports whether debugfs `stat` said the path does not exist. debugfs
// exits 0 for a failed scripted command, so this is only visible in its output —
// e.g. "/usr/bin/envd: File not found by ext2_lookup".
func envdAbsent(statOut string) bool {
	l := strings.ToLower(statOut)

	return strings.Contains(l, "file not found") || strings.Contains(l, "no such file")
}

// debugfsModeRe extracts the octal permission bits from a debugfs `stat` header
// line, e.g. "Inode: 12   Type: regular    Mode:  0755   Flags: 0x0".
var debugfsModeRe = regexp.MustCompile(`Mode:\s*0*([0-7]{3,4})`)

// debugfsSizeRe extracts the byte size from a debugfs `stat` header line, e.g.
// "User: 0   Group: 0   Project: 0   Size: 18967672". The later "Size of extra
// inode fields: 32" line does not match — there the key is not "Size:".
var debugfsSizeRe = regexp.MustCompile(`\bSize:\s*([0-9]+)`)

// statEnvdSize reports the size of /usr/bin/envd inside the rootfs, read from the
// inode via debugfs `stat` — no dump, so nothing proportional to the size crosses
// into the host. An unreadable or unparseable stat is an error, not a pass: a
// well-formed rootfs always has a statable envd, and this runs before any mutation,
// so refusing costs only the upgrade (the guest boots its own envd).
func statEnvdSize(ctx context.Context, dbg swapIO) (int64, error) {
	out, err := dbg.run(ctx, "size-stat",
		fmt.Sprintf("stat %s\n", guestEnvdPath), false)
	if err != nil {
		return 0, fmt.Errorf("stat for size check: %w (output: %q)", err, string(out))
	}

	return parseEnvdSize(string(out))
}

// parseEnvdSize pulls the inode size out of debugfs `stat` output.
func parseEnvdSize(statOut string) (int64, error) {
	m := debugfsSizeRe.FindStringSubmatch(statOut)
	if m == nil {
		return 0, fmt.Errorf("no Size field in debugfs stat output (%q)", statOut)
	}
	size, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse size %q: %w", m[1], err)
	}

	return size, nil
}

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
//
// The dump target is pre-truncated and debugfs exits 0 even when the scripted `dump`
// failed — the very failure mode this primitive is built around — so an empty file
// here is a FAILED read, not a zero-length binary, and hashing it would hand back the
// empty-file digest as though it were real rootfs content. Reject that instead, the
// same way the backup does: no bootable envd is zero bytes.
func dumpSHA256(ctx context.Context, dbg swapIO, phase string) (string, error) {
	out := filepath.Join(dbg.stageDir, "envd."+phase)
	// Pre-create world-writable so the jailed DynamicUser can write the dump into
	// the root-owned stage dir (see createJailWritable).
	if err := createJailWritable(out); err != nil {
		return "", fmt.Errorf("pre-create dump target: %w", err)
	}
	if o, err := dbg.run(ctx, phase,
		fmt.Sprintf("dump %s %s\n", guestEnvdPath, out), false); err != nil {
		return "", fmt.Errorf("dump %s: %w (output: %q)", guestEnvdPath, err, string(o))
	}
	fi, err := os.Stat(out)
	if err != nil {
		return "", fmt.Errorf("stat dump of %s: %w", guestEnvdPath, err)
	}
	if fi.Size() == 0 {
		return "", fmt.Errorf("dump of %s produced no bytes (silent debugfs failure)", guestEnvdPath)
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
