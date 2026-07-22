package process

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/e2b-dev/infra/packages/envd/internal/services/cgroups"
	"github.com/e2b-dev/infra/packages/envd/internal/services/process/handler"
	rpc "github.com/e2b-dev/infra/packages/envd/internal/services/spec/process"
	"github.com/e2b-dev/infra/packages/envd/pkg"
)

// HandoverPath is the tmpfs blob the outgoing envd writes and the incoming one
// reads across a live self-upgrade. The format is JSON with only additive
// fields, so an outgoing and incoming envd built at different versions stay
// compatible. /run is tmpfs, so it never touches the rootfs diff. A var (not a
// const) so tests can point it at a temp file.
var HandoverPath = "/run/e2b/envd-handover.json"

// fdBase is where carried fds are dup3'd to; high enough that the fresh runtime
// in the new image won't have grabbed these numbers during early startup.
const fdBase = 200

// DefaultUpgradeBinPath is the only filesystem path a live upgrade will write a
// delivered binary to and re-exec into. Constraining the target (rather than
// trusting a request header or marker file) keeps a malformed or forged upgrade
// request from writing to / executing an arbitrary path. The /upgrade endpoint
// is authenticated, but this is defense-in-depth around a same-PID exec.
const DefaultUpgradeBinPath = "/usr/bin/envd.next"

type handoverProc struct {
	Pid      uint32 `json:"pid"`
	Tag      string `json:"tag"`
	HasTag   bool   `json:"has_tag"`
	CgType   string `json:"cg_type"`
	ConfigPB []byte `json:"config_pb"` // protojson of rpc.ProcessConfig
	StdoutFD int    `json:"stdout_fd"`
	StderrFD int    `json:"stderr_fd"`
	StdinFD  int    `json:"stdin_fd"`
	TtyFD    int    `json:"tty_fd"`
	// TimeoutMs is the process's remaining timeout at handover time: 0 means no
	// timeout, >0 means kill after that many ms (re-armed in Readopt). Carried
	// so a timed-out process is still killed after the upgrade.
	TimeoutMs int64 `json:"timeout_ms"`
}

// handoverExit is a recently-exited process's retained terminal event, carried
// so a Connect after the upgrade can still recover the exit code of a process
// that exited shortly BEFORE the upgrade (it lives only in the retention cache,
// not the live process table).
type handoverExit struct {
	Pid         uint32 `json:"pid"`
	Tag         string `json:"tag"`
	HasTag      bool   `json:"has_tag"`
	EndPB       []byte `json:"end_pb"` // protojson of rpc.ProcessEvent_EndEvent
	RemainingMs int64  `json:"remaining_ms"`
}

type handoverState struct {
	FromVer   string         `json:"from_ver"`
	Processes []handoverProc `json:"processes"`
	// Terminated carries the retention cache (recently-exited terminal events).
	Terminated []handoverExit `json:"terminated,omitempty"`
	// Watchers is an opaque blob owned by the filesystem service (its watcher
	// state, re-armed after the upgrade).
	Watchers json.RawMessage `json:"watchers,omitempty"`
}

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
func (s *Service) Upgrade(newBin, fromVer string, watchersJSON []byte) error {
	// Only re-exec self (empty) or the fixed delivered-binary path — never an
	// arbitrary caller-supplied path. Checked first, before any side effects.
	if newBin != "" && newBin != DefaultUpgradeBinPath {
		return fmt.Errorf("refusing upgrade to unexpected binary %q", newBin)
	}

	st := handoverState{FromVer: fromVer, Watchers: json.RawMessage(watchersJSON)}

	// Serialize the process table and relocate each carried fd to its fixed
	// target, holding syscall.ForkLock so no concurrent Go fd allocation can
	// claim a target between dupKeep's free-check and its dup3. A collision or
	// dup3 failure aborts the upgrade with the workload intact (handled below).
	var (
		dupErr error
		dupped []int
	)
	i := 0
	syscall.ForkLock.Lock()
	s.processes.Range(func(_ uint32, h *handler.Handler) bool {
		stdout, stderr, stdin, tty := h.HandoverFds()
		slot := fdBase + i*5

		cfgPB, _ := protojson.Marshal(h.Config)
		hp := handoverProc{
			Pid:      h.Pid(),
			CgType:   string(h.CgType()),
			ConfigPB: cfgPB,
		}
		for _, m := range []struct {
			old int
			tgt int
			dst *int
		}{
			{stdout, slot + 0, &hp.StdoutFD},
			{stderr, slot + 1, &hp.StderrFD},
			{stdin, slot + 2, &hp.StdinFD},
			{tty, slot + 3, &hp.TtyFD},
		} {
			fd, err := dupKeep(m.old, m.tgt)
			if err != nil {
				dupErr = err

				return false
			}
			*m.dst = fd
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
	}()

	// Carry the retention cache so a process that exited shortly before the
	// upgrade keeps its exit code retrievable on the new envd.
	s.terminated.Range(func(pid uint32, r *retainedExit) bool {
		rem := time.Until(r.expiry).Milliseconds()
		if rem <= 0 {
			return true
		}

		endPB, _ := protojson.Marshal(r.end)
		he := handoverExit{Pid: pid, EndPB: endPB, RemainingMs: rem}
		if r.tag != nil {
			he.Tag = *r.tag
			he.HasTag = true
		}
		st.Terminated = append(st.Terminated, he)

		return true
	})

	blob, err := json.Marshal(st)
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
}

func (s *Service) ResumeFromHandover(reArmWatchers func([]byte) (rearmed, failed int)) (HandoverResult, error) {
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

	var st handoverState
	if err := json.Unmarshal(blob, &st); err != nil {
		return HandoverResult{}, fmt.Errorf("unmarshal handover: %w", err)
	}

	// journald-visible proof of the running image after the swap (from_ver is
	// the outgoing version; pkg.Version is what this new image is).
	fmt.Fprintf(os.Stderr, "envd: resumed as v%s after handover (from v%s, %d procs)\n",
		pkg.Version, st.FromVer, len(st.Processes))

	fileOrNil := func(fd int, name string) *os.File {
		if fd < 0 {
			return nil
		}

		return os.NewFile(uintptr(fd), name)
	}

	procsFailed := 0
	for _, hp := range st.Processes {
		cfg := &rpc.ProcessConfig{}
		if len(hp.ConfigPB) > 0 {
			if err := protojson.Unmarshal(hp.ConfigPB, cfg); err != nil {
				// The process is still re-adopted (with a default config) — a
				// degradation, not a drop — but count it as a handover failure.
				s.logger.Error().Err(err).Uint32("pid", hp.Pid).Msg("handover: bad config")
				procsFailed++
			}
		}

		var tag *string
		if hp.HasTag {
			t := hp.Tag
			tag = &t
		}

		var timeout time.Duration
		if hp.TimeoutMs > 0 {
			timeout = time.Duration(hp.TimeoutMs) * time.Millisecond
		}

		h := handler.Readopt(handler.ReadoptArgs{
			Pid:     hp.Pid,
			Tag:     tag,
			Config:  cfg,
			CgType:  cgroups.ProcessType(hp.CgType),
			Stdout:  fileOrNil(hp.StdoutFD, fmt.Sprintf("p%d-stdout", hp.Pid)),
			Stderr:  fileOrNil(hp.StderrFD, fmt.Sprintf("p%d-stderr", hp.Pid)),
			Stdin:   fileOrNil(hp.StdinFD, fmt.Sprintf("p%d-stdin", hp.Pid)),
			Tty:     fileOrNil(hp.TtyFD, fmt.Sprintf("p%d-tty", hp.Pid)),
			Timeout: timeout,
		}, s.logger)

		pid := hp.Pid
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
			Uint32("pid", hp.Pid).
			Msg("re-adopted process after envd self-upgrade")
	}

	// Restore the retention cache: terminal events of processes that exited
	// shortly before the upgrade, so a Connect can still recover their exit code.
	retainedFailed := s.restoreTerminated(st.Terminated)

	_ = os.Remove(HandoverPath)

	// Re-arm filesystem watchers while the workload is STILL frozen (it stays
	// frozen past this return until the post-upgrade /init thaws it), so no
	// filesystem event is lost in the gap between the thaw and the re-arm.
	watchersRearmed, watchersFailed := 0, 0
	if reArmWatchers != nil {
		watchersRearmed, watchersFailed = reArmWatchers(st.Watchers)
	}

	// Loki-queryable summary of what the handover carried + how it fared
	// (rollout observability; the counts also ride to the orchestrator via the
	// /init X-Envd-Handover header — see HandoverResult).
	s.logger.Info().
		Str("event_type", "handover_resumed").
		Str("from_ver", st.FromVer).
		Int("procs", len(st.Processes)).
		Int("procs_failed", procsFailed).
		Int("retained", len(st.Terminated)).
		Int("retained_failed", retainedFailed).
		Int("watchers", watchersRearmed+watchersFailed).
		Int("watchers_failed", watchersFailed).
		Msg("re-adopted workload after envd self-upgrade")

	// Handover succeeded: keep the workload frozen (see the deferred thaw above);
	// the orchestrator's post-upgrade /init thaws it once auth is restored.
	keepFrozen = true

	return HandoverResult{
		Procs:          len(st.Processes),
		ProcsFailed:    procsFailed,
		Retained:       len(st.Terminated),
		RetainedFailed: retainedFailed,
		Watchers:       watchersRearmed + watchersFailed,
		WatchersFailed: watchersFailed,
	}, nil
}

// restoreTerminated re-populates the terminal-event retention cache from the
// handover. It skips any PID that was already re-adopted as a *live* process
// above: a PID can't be both live and terminated, and caching a stale exit
// under a live PID could let a Connect (or a later reuse of that PID) be served
// the wrong exit code. Mirrors handleStart's clear-on-register.
func (s *Service) restoreTerminated(entries []handoverExit) (failed int) {
	for _, he := range entries {
		if _, live := s.processes.Load(he.Pid); live {
			continue
		}

		end := &rpc.ProcessEvent_EndEvent{}
		if len(he.EndPB) > 0 {
			if err := protojson.Unmarshal(he.EndPB, end); err != nil {
				s.logger.Error().Err(err).Uint32("pid", he.Pid).Msg("handover: bad retained exit")
				failed++

				continue
			}
		}

		var tag *string
		if he.HasTag {
			t := he.Tag
			tag = &t
		}

		s.retain(he.Pid, &retainedExit{
			pid:    he.Pid,
			tag:    tag,
			end:    end,
			expiry: time.Now().Add(time.Duration(he.RemainingMs) * time.Millisecond),
		})
	}

	return failed
}

// FreezeWorkload freezes the user/pty cgroups ahead of an Upgrade, serialized
// (via the shared freezer) against the HTTP API's freeze/unfreeze paths.
func (s *Service) FreezeWorkload() {
	if err := s.workloadFreezer.Freeze(context.Background()); err != nil {
		s.logger.Warn().Err(err).Msg("handover: freeze failed")
	}
}

// FreezeWorkloadHold freezes the workload and keeps the shared freeze lock held,
// returning a release func, so the freeze stays uninterruptible across the
// handover: a concurrent /init or /unfreeze thaw blocks until release. The caller
// MUST release on any path that does not execve; a successful execve drops the
// lock with the process image.
func (s *Service) FreezeWorkloadHold() (release func()) {
	release, err := s.workloadFreezer.FreezeHold(context.Background())
	if err != nil {
		s.logger.Warn().Err(err).Msg("handover: freeze failed")
	}

	return release
}

// UnfreezeWorkload thaws the user/pty cgroups. Idempotent (thawing a non-frozen
// cgroup is a no-op), so it is safe to call on every upgrade outcome — success,
// failure, or panic — guaranteeing a failed swap never leaves the workload frozen.
func (s *Service) UnfreezeWorkload() {
	if err := s.workloadFreezer.Unfreeze(context.Background()); err != nil {
		s.logger.Warn().Err(err).Msg("handover: unfreeze failed")
	}
}
