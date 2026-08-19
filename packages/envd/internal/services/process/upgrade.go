package process

import (
	"context"
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
	"google.golang.org/protobuf/proto"

	"github.com/e2b-dev/infra/packages/envd/internal/services/cgroups"
	"github.com/e2b-dev/infra/packages/envd/internal/services/process/handler"
	rpc "github.com/e2b-dev/infra/packages/envd/internal/services/spec/process"
	"github.com/e2b-dev/infra/packages/envd/internal/services/spec/upgrade"
	"github.com/e2b-dev/infra/packages/envd/pkg"
)

// HandoverPath is the tmpfs blob the outgoing envd writes and the incoming one
// reads across a live self-upgrade. The format is a protobuf-encoded
// upgrade.HandoverState (spec/upgrade/handover.proto) — an additive schema, so
// an outgoing and incoming envd built at different versions stay compatible.
// /run is tmpfs, so it never touches the rootfs diff. A var (not a const) so
// tests can point it at a temp file.
var HandoverPath = "/run/e2b/envd-handover.pb"

// handoverSchema is the version of the HandoverState layout this envd writes and
// the maximum it will read. A reader refuses a blob whose schema exceeds this
// (design §6.4: decode every schema <= N, abort on schema > N) rather than
// mis-read a newer-than-known layout — the outgoing envd then keeps running the
// old binary and the resume proceeds unupgraded.
const handoverSchema = 1

// fdBase is where carried fds are dup3'd to; high enough that the fresh runtime
// in the new image won't have grabbed these numbers during early startup.
const fdBase = 200

// DefaultUpgradeBinPath is the only filesystem path a live upgrade will write a
// delivered binary to and re-exec into. Constraining the target (rather than
// trusting a request header or marker file) keeps a malformed or forged upgrade
// request from writing to / executing an arbitrary path. The /upgrade endpoint
// is authenticated, but this is defense-in-depth around a same-PID exec.
const DefaultUpgradeBinPath = "/usr/bin/envd.next"

// dupKeep dup3's oldfd onto target with CLOEXEC cleared so it survives execve.
// Returns (target, nil) on success and (-1, nil) when oldfd is absent. It errors
// if target is already open: dup3 silently closes the occupant, so a target
// collision must abort the upgrade rather than corrupt a live fd. The caller
// holds syscall.ForkLock, so no concurrent Go fd allocation can claim target
// between the F_GETFD check and the dup3.
func dupKeep(oldfd, target int) (int, error) {
	if oldfd < 0 {
		return -1, nil
	}
	if _, err := unix.FcntlInt(uintptr(target), unix.F_GETFD, 0); err == nil {
		return -1, fmt.Errorf("handover fd target %d already in use", target)
	}
	if err := dup3(oldfd, target, 0); err != nil {
		return -1, fmt.Errorf("dup3 %d->%d: %w", oldfd, target, err)
	}

	return target, nil
}

// Upgrade is the outgoing side of a live self-upgrade. It must be
// called with the workload frozen and envd's own spawners quiesced (caller's
// responsibility — see main's /upgrade handler). It serializes the process
// table, carries the I/O fds across execve, and re-execs newBin with the same
// PID. It does not return on success.
func (s *Service) Upgrade(newBin, fromVer string, watchers []*upgrade.HandoverWatcher, mounts []*upgrade.MountEntry, forwards []*upgrade.ForwardedPort) error {
	// Only re-exec self (empty) or the fixed delivered-binary path — never an
	// arbitrary caller-supplied path. Checked first, before any side effects.
	if newBin != "" && newBin != DefaultUpgradeBinPath {
		return fmt.Errorf("refusing upgrade to unexpected binary %q", newBin)
	}

	st := &upgrade.HandoverState{
		Schema:   handoverSchema,
		FromVer:  fromVer,
		Watchers: watchers,
		Mounts:   mounts,
		Forwards: forwards,
	}

	// Serialize the process table and relocate each carried fd to its fixed
	// target, holding syscall.ForkLock so no concurrent Go fd allocation can
	// claim a target between dupKeep's free-check and its dup3. A collision or
	// dup3 failure aborts the upgrade with the workload intact (handled below).
	var (
		dupErr error
		dupped []int
	)
	i := 0
	// snapshotMu (write) blocks concurrent Start registration for the whole
	// snapshot→execve window, so no child is spawned-but-unregistered (and thus
	// left behind, unconnectable, across the swap). Taken before ForkLock and
	// released on every exit path alongside it — consistent order, no deadlock
	// (handleStart takes RLock then ForkLock via fork; Upgrade takes them in the
	// same order).
	s.snapshotMu.Lock()
	syscall.ForkLock.Lock()
	s.processes.Range(func(_ uint32, h *handler.Handler) bool {
		stdout, stderr, stdin, tty := h.HandoverFds()
		slot := fdBase + i*5

		hp := &upgrade.HandoverProc{
			Pid:    h.Pid(),
			CgType: string(h.CgType()),
			// Native nested message — no protojson round-trip. The live
			// *rpc.ProcessConfig is carried directly and proto.Marshal encodes it.
			Config: h.Config,
		}
		for _, m := range []struct {
			old int
			tgt int
			dst *int32
		}{
			{stdout, slot + 0, &hp.StdoutFd},
			{stderr, slot + 1, &hp.StderrFd},
			{stdin, slot + 2, &hp.StdinFd},
			{tty, slot + 3, &hp.TtyFd},
		} {
			fd, err := dupKeep(m.old, m.tgt)
			if err != nil {
				dupErr = err

				return false
			}
			*m.dst = int32(fd)
			if fd >= 0 {
				dupped = append(dupped, fd)
			}
		}
		// Carry the remaining timeout so it is re-armed on the new envd. A
		// deadline already in the past is clamped to 1ms (kill ASAP).
		if d, ok := h.Deadline(); ok {
			if rem := time.Until(d).Milliseconds(); rem > 0 {
				hp.TimeoutMs = rem
			} else {
				hp.TimeoutMs = 1
			}
		}
		if h.Tag != nil {
			hp.Tag = *h.Tag
			hp.HasTag = true
		}
		st.Processes = append(st.Processes, hp)
		i++

		return true
	})
	if dupErr != nil {
		// Close the fds already relocated so a retry sees a clean target window,
		// then keep running the old binary.
		for _, fd := range dupped {
			_ = unix.Close(fd)
		}
		syscall.ForkLock.Unlock()
		s.snapshotMu.Unlock()

		return fmt.Errorf("relocate handover fds: %w", dupErr)
	}

	// Keep ForkLock held from the CLOEXEC-clearing relocation above all the way
	// through the execve below. Dropping it here would leave the carried fds
	// (now CLOEXEC-cleared) exposed to a concurrent os/exec fork — the port
	// scanner's socat, an in-flight Start — which would inherit them. The
	// intervening marshal / write / os.Executable never fork, so holding it is
	// safe. On any error return before the execve (which never returns on
	// success — it replaces the image), close the relocated dups so they don't
	// leak into the still-running old envd and its future children, and release
	// the lock. Mirrors the dupErr cleanup above.
	defer func() {
		for _, fd := range dupped {
			_ = unix.Close(fd)
		}
		syscall.ForkLock.Unlock()
		s.snapshotMu.Unlock()
	}()

	// Carry the retention cache so a process that exited shortly before the
	// upgrade keeps its exit code retrievable on the new envd.
	s.terminated.Range(func(pid uint32, r *retainedExit) bool {
		rem := time.Until(r.expiry).Milliseconds()
		if rem <= 0 {
			return true
		}

		he := &upgrade.HandoverExit{Pid: pid, End: r.end, RemainingMs: rem}
		if r.tag != nil {
			he.Tag = *r.tag
			he.HasTag = true
		}
		st.Terminated = append(st.Terminated, he)

		return true
	})

	blob, err := proto.Marshal(st)
	if err != nil {
		return fmt.Errorf("marshal handover: %w", err)
	}
	if err := os.WriteFile(HandoverPath, blob, 0o600); err != nil {
		return fmt.Errorf("write handover: %w", err)
	}

	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable: %w", err)
	}
	if newBin == "" {
		newBin = exe
	}

	// argv: keep original flags, append --resume-handover.
	argv := append([]string{newBin}, os.Args[1:]...)
	argv = append(argv, "--resume-handover")

	// Carry the dup'd fds across execve. ForkLock is still held from the
	// relocation above (released by the deferred cleanup), so no concurrent fork
	// can inherit the CLOEXEC-cleared fds. Exec only returns on failure
	// (corrupt/missing staged binary); the deferred cleanup then closes the
	// relocated dups and releases the lock while the old envd keeps running.
	err = syscall.Exec(newBin, argv, os.Environ())

	return fmt.Errorf("execve %s: %w", newBin, err)
}

// ResumeFromHandover is the incoming side: read the blob, re-adopt
// each process from its inherited fds, register it, then thaw the workload.
// No-op if no handover file is present.
// It returns the opaque filesystem-watcher blob (if any) so the caller can hand
// it to the filesystem service to re-arm watches.
// HandoverResult summarizes a completed incoming handover. It is surfaced to the
// orchestrator via the /init X-Envd-Handover header so the envd-side outcome
// (which envd otherwise only logs) is observable fleet-wide.
type HandoverResult struct {
	// Every item is total-carried + failed-subset (ok = total - failed).
	Procs          int
	ProcsFailed    int
	Retained       int
	RetainedFailed int
	Watchers       int
	WatchersFailed int
	// Mounts and Forwards are decoded from the blob and handed back to their
	// owners (the API service's mount ledger and the port forwarder), which are
	// constructed after ResumeFromHandover runs — so unlike watchers they are
	// returned rather than applied via a callback.
	Mounts   []*upgrade.MountEntry
	Forwards []*upgrade.ForwardedPort
}

func (s *Service) ResumeFromHandover(reArmWatchers func([]*upgrade.HandoverWatcher) (rearmed, failed int)) (HandoverResult, error) {
	// Thaw the workload on every FAILURE path — a bad blob, partial re-adopt, or
	// panic must never leave the sandbox frozen (a degraded-but-running workload
	// beats a hung one). On SUCCESS the workload is deliberately left frozen: the
	// orchestrator's post-upgrade /init thaws it (deferred unfreeze in PostInit)
	// only after it has re-established the access token. This closes the window
	// in which a re-adopted — and possibly hostile — guest process could run
	// before /init restores auth and reach the unauthenticated /upgrade endpoint
	// (which execs request-body bytes as root).
	keepFrozen := false
	defer func() {
		if !keepFrozen {
			s.UnfreezeWorkload()
		}
	}()

	blob, err := os.ReadFile(HandoverPath)
	if os.IsNotExist(err) {
		return HandoverResult{}, nil
	}
	if err != nil {
		return HandoverResult{}, fmt.Errorf("read handover: %w", err)
	}

	st := &upgrade.HandoverState{}
	if err := proto.Unmarshal(blob, st); err != nil {
		return HandoverResult{}, fmt.Errorf("unmarshal handover: %w", err)
	}

	// Schema gate (design §6.4): refuse a blob written by a newer envd whose
	// layout this binary does not understand, rather than mis-read it. The
	// outgoing envd is already gone (this runs post-execve), so we can't fall
	// back to it — but the orchestrator's post-upgrade /init readiness wait will
	// fail and the resume is reported unupgraded rather than corrupt.
	if st.GetSchema() > handoverSchema {
		return HandoverResult{}, fmt.Errorf("handover schema %d exceeds max supported %d", st.GetSchema(), handoverSchema)
	}

	// journald-visible proof of the running image after the swap (from_ver is
	// the outgoing version; pkg.Version is what this new image is).
	fmt.Fprintf(os.Stderr, "envd: resumed as v%s after handover (from v%s, %d procs)\n",
		pkg.Version, st.GetFromVer(), len(st.GetProcesses()))

	// fileOrNil wraps a carried fd (inherited at its fixed fdBase slot with
	// CLOEXEC cleared, from the outgoing envd's execve) as an *os.File, but first
	// RELOCATES it off the fdBase range onto a fresh fd with CLOEXEC set. This
	// frees the slot for the NEXT upgrade's relocation: a chained upgrade dup3's
	// the carried fds back onto the same fdBase slots, and dupKeep aborts if a
	// target is already in use — so leaving them parked at fdBase would break the
	// second swap. Setting CLOEXEC also stops the fd from leaking into processes
	// this envd spawns before the next upgrade (the next dupKeep re-clears it).
	fileOrNil := func(fd int, name string) *os.File {
		if fd < 0 {
			return nil
		}

		fresh, err := unix.Dup(fd)
		if err != nil {
			// Fall back to the slot as-is: the current re-adoption still works;
			// only a later chained upgrade might collide.
			s.logger.Warn().Err(err).Int("fd", fd).Msg("handover: relocate carried fd off fdBase failed")

			return os.NewFile(uintptr(fd), name)
		}
		unix.CloseOnExec(fresh)
		_ = unix.Close(fd)

		return os.NewFile(uintptr(fresh), name)
	}

	// Captured before re-adoption (while the workload is still frozen): closed
	// when the post-upgrade /init — or the fallback — next thaws the workload, so
	// each re-adopted process's carried kill-timer only starts counting once the
	// process can actually run again.
	thawed := s.workloadFreezer.Thawed()

	procsFailed := 0
	for _, hp := range st.GetProcesses() {
		// Native nested message — carried directly by proto, no protojson
		// round-trip. A malformed config would have failed the top-level
		// proto.Unmarshal above (schema-gated), so here it is either the real
		// config or absent; default an absent one so Readopt always gets non-nil.
		cfg := hp.GetConfig()
		if cfg == nil {
			cfg = &rpc.ProcessConfig{}
		}

		var tag *string
		if hp.GetHasTag() {
			t := hp.GetTag()
			tag = &t
		}

		var timeout time.Duration
		if hp.GetTimeoutMs() > 0 {
			timeout = time.Duration(hp.GetTimeoutMs()) * time.Millisecond
		}

		h := handler.Readopt(handler.ReadoptArgs{
			Pid:     hp.GetPid(),
			Tag:     tag,
			Config:  cfg,
			CgType:  cgroups.ProcessType(hp.GetCgType()),
			Stdout:  fileOrNil(int(hp.GetStdoutFd()), fmt.Sprintf("p%d-stdout", hp.GetPid())),
			Stderr:  fileOrNil(int(hp.GetStderrFd()), fmt.Sprintf("p%d-stderr", hp.GetPid())),
			Stdin:   fileOrNil(int(hp.GetStdinFd()), fmt.Sprintf("p%d-stdin", hp.GetPid())),
			Tty:     fileOrNil(int(hp.GetTtyFd()), fmt.Sprintf("p%d-tty", hp.GetPid())),
			Timeout: timeout,
			Thawed:  thawed,
		}, s.logger)

		pid := hp.GetPid()
		s.processes.Store(pid, h)
		// Retain the terminal event synchronously on exit, before the reaper
		// closes EndEvent, so a Connect arriving in the handover gap always
		// recovers the exit code even if it forks after the close. Set before
		// BeginReaping: a process whose timeout expired during the freeze can
		// exit the instant it is unfrozen, so the reaper — which invokes the
		// hook — must never start before the hook is in place.
		h.OnExit = func(end *rpc.ProcessEvent_EndEvent) {
			s.finalizeTermination(pid, h, end)
		}
		h.BeginReaping()

		s.logger.Info().
			Str("event_type", "process_readopted").
			Uint32("pid", hp.GetPid()).
			Msg("re-adopted process after envd self-upgrade")
	}

	// Restore the retention cache: terminal events of processes that exited
	// shortly before the upgrade, so a Connect can still recover their exit code.
	retainedFailed := s.restoreTerminated(st.GetTerminated())

	_ = os.Remove(HandoverPath)

	// Re-arm filesystem watchers while the workload is STILL frozen (it stays
	// frozen past this return until the post-upgrade /init thaws it), so no
	// filesystem event is lost in the gap between the thaw and the re-arm.
	watchersRearmed, watchersFailed := 0, 0
	if reArmWatchers != nil {
		watchersRearmed, watchersFailed = reArmWatchers(st.GetWatchers())
	}

	// Loki-queryable summary of what the handover carried + how it fared
	// (rollout observability; the counts also ride to the orchestrator via the
	// /init X-Envd-Handover header — see HandoverResult).
	s.logger.Info().
		Str("event_type", "handover_resumed").
		Str("from_ver", st.GetFromVer()).
		Int("procs", len(st.GetProcesses())).
		Int("procs_failed", procsFailed).
		Int("retained", len(st.GetTerminated())).
		Int("retained_failed", retainedFailed).
		Int("watchers", watchersRearmed+watchersFailed).
		Int("watchers_failed", watchersFailed).
		Msg("re-adopted workload after envd self-upgrade")

	// Handover succeeded: keep the workload frozen (see the deferred thaw above);
	// the orchestrator's post-upgrade /init thaws it once auth is restored.
	keepFrozen = true

	return HandoverResult{
		Procs:          len(st.GetProcesses()),
		ProcsFailed:    procsFailed,
		Retained:       len(st.GetTerminated()),
		RetainedFailed: retainedFailed,
		Watchers:       watchersRearmed + watchersFailed,
		WatchersFailed: watchersFailed,
		// Handed back to the API service (mount ledger) and port forwarder, which
		// are constructed after this returns.
		Mounts:   st.GetMounts(),
		Forwards: st.GetForwards(),
	}, nil
}

// restoreTerminated re-populates the terminal-event retention cache from the
// handover. It skips any PID that was already re-adopted as a *live* process
// above: a PID can't be both live and terminated, and caching a stale exit
// under a live PID could let a Connect (or a later reuse of that PID) be served
// the wrong exit code. Mirrors handleStart's clear-on-register.
func (s *Service) restoreTerminated(entries []*upgrade.HandoverExit) (failed int) {
	for _, he := range entries {
		if _, live := s.processes.Load(he.GetPid()); live {
			continue
		}

		// Native nested message — carried directly by proto (schema-gated at
		// decode). Default an absent event so retain always gets a non-nil End.
		end := he.GetEnd()
		if end == nil {
			end = &rpc.ProcessEvent_EndEvent{}
		}

		var tag *string
		if he.GetHasTag() {
			t := he.GetTag()
			tag = &t
		}

		s.retain(he.GetPid(), &retainedExit{
			pid:    he.GetPid(),
			tag:    tag,
			end:    end,
			expiry: time.Now().Add(time.Duration(he.GetRemainingMs()) * time.Millisecond),
		})
	}

	return failed
}

// ErrWorkloadNotFrozen means the pre-handover freeze was issued but the workload
// had not stopped by the deadline, so the swap was refused.
var ErrWorkloadNotFrozen = errors.New("workload was not frozen before the handover")

// FreezeWorkloadHold freezes the workload and keeps the shared freeze lock held,
// returning a release func, so the freeze stays uninterruptible across the
// handover: a concurrent /init or /unfreeze thaw blocks until release. The caller
// MUST release on any path that does not execve; a successful execve drops the
// lock with the process image.
// Unlike the pause path, this one is STRICT: if the workload did not fully stop
// the handover is refused. execve-ing over a still-running workload is exactly what
// the freeze exists to prevent, and unlike a pause the handover has a safe
// alternative -- abort and thaw, leaving a working envd in place.
func (s *Service) FreezeWorkloadHold() (release func(), err error) {
	release, res, err := s.workloadFreezer.FreezeHold(context.Background(), s.handoverMaxWait)
	if err != nil {
		s.logger.Warn().Err(err).Msg("handover: freeze failed")

		return release, err
	}
	if !res.AllFrozen() {
		s.logger.Warn().
			Int("frozen", res.Frozen).
			Int("not_frozen", res.NotFrozen).
			Int("failed", res.Failed).
			Dur("wait_duration", res.WaitDuration).
			Msg("handover: refusing to swap over a workload that did not stop")

		return release, ErrWorkloadNotFrozen
	}

	return release, nil
}

// UnfreezeWorkload thaws the user/pty cgroups. Idempotent (thawing a non-frozen
// cgroup is a no-op), so it is safe to call on every upgrade outcome — success,
// failure, or panic — guaranteeing a failed swap never leaves the workload frozen.
func (s *Service) UnfreezeWorkload() {
	if err := s.workloadFreezer.Unfreeze(context.Background()); err != nil {
		s.logger.Warn().Err(err).Msg("handover: unfreeze failed")
	}
}
