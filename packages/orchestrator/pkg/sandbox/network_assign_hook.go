//go:build linux

package sandbox

import "context"

// NetworkAssignReason tells NetworkAssignHook.OnNetworkAssign what kind of
// start this is (a brand-new sandbox, a memory-snapshot resume, etc.), so
// an implementation can decide what to do based on it (e.g. only guard
// against a resumed guest's traffic, and skip that work on a fresh boot).
type NetworkAssignReason uint8

const (
	// NetworkAssignReasonUnknown is the zero value: an omitted or not-yet-set
	// reason, distinct from every deliberately chosen one.
	NetworkAssignReasonUnknown NetworkAssignReason = iota
	// NetworkAssignReasonCreate: a brand-new sandbox, cold-booting for the first time.
	NetworkAssignReasonCreate
	// NetworkAssignReasonResume: a memory-snapshot resume; guest state (including any
	// established network connection) may be preserved across this call.
	NetworkAssignReasonResume
	// NetworkAssignReasonReboot: a filesystem-only snapshot boot; guest
	// memory, and therefore any prior network connection state, is not
	// preserved.
	NetworkAssignReasonReboot
	// NetworkAssignReasonThrowawayResume: a short-lived resume that only
	// exists to prefetch a sandbox's resume working set into the page cache
	// ahead of time, so a later real resume is faster (see
	// ThrowawayResumeOptions). The caller reaps it itself right after, so it
	// is never promoted to a live, routable sandbox (WithoutLiveRegistration)
	// and stays network-isolated for its entire lifetime (WithDenyEgress),
	// since nothing should ever reach it or be reached by it.
	NetworkAssignReasonThrowawayResume
)

// String implements fmt.Stringer so a NetworkAssignReason logged via zap.Any (or
// any other %v/%s formatting) reads as a name, not a bare uint8.
func (r NetworkAssignReason) String() string {
	switch r {
	case NetworkAssignReasonCreate:
		return "create"
	case NetworkAssignReasonResume:
		return "resume"
	case NetworkAssignReasonReboot:
		return "reboot"
	case NetworkAssignReasonThrowawayResume:
		return "throwaway_resume"
	default:
		return "unknown"
	}
}

// NetworkAssignHook is an optional extension point for sandbox startup. It
// is called once per start attempt, right after AssignNetwork puts the
// sandbox in the network map and strictly before that sandbox's guest can
// run (or resume running) for this attempt. It's a no-op by default (see
// NoopNetworkAssignHook).
type NetworkAssignHook interface {
	OnNetworkAssign(ctx context.Context, sbx *Sandbox, reason NetworkAssignReason) error
}

// NoopNetworkAssignHook is a no-op NetworkAssignHook, used when no
// edition-specific implementation is configured.
type NoopNetworkAssignHook struct{}

var _ NetworkAssignHook = NoopNetworkAssignHook{}

func (NoopNetworkAssignHook) OnNetworkAssign(_ context.Context, _ *Sandbox, _ NetworkAssignReason) error {
	return nil
}
