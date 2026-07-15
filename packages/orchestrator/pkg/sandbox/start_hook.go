//go:build linux

package sandbox

import "context"

// StartReason identifies why a sandbox is starting. It carries no
// information beyond that; it exists so an edition-specific StartHook can
// decide, per call, whether local, bounded preparatory work is justified —
// infra itself has no opinion on what that work is or why the distinction
// matters to any given implementation.
//
// Values are opaque and must be compared only by name — never persisted,
// logged raw, or depended on numerically. New reasons are appended, never
// inserted or reordered.
type StartReason uint8

const (
	// StartReasonUnknown is the zero value: an omitted or not-yet-set
	// reason, distinct from every deliberately chosen one.
	StartReasonUnknown StartReason = iota
	// StartReasonCreate: a brand-new sandbox, cold-booting for the first time.
	StartReasonCreate
	// StartReasonResume: a memory-snapshot resume; guest state (including any
	// established network connection) may be preserved across this call.
	StartReasonResume
	// StartReasonReboot: a cold boot from a filesystem-only snapshot; guest
	// memory, and therefore any prior network connection state, is not
	// preserved.
	StartReasonReboot
	// StartReasonThrowawayResume: a resume that will not be promoted to a
	// live, routable sandbox (see WithoutLiveRegistration) and whose network
	// egress is denied for its entire lifetime (see WithDenyEgress).
	StartReasonThrowawayResume
)

// String implements fmt.Stringer so a StartReason logged via zap.Any (or
// any other %v/%s formatting) reads as a name, not a bare uint8.
func (r StartReason) String() string {
	switch r {
	case StartReasonCreate:
		return "create"
	case StartReasonResume:
		return "resume"
	case StartReasonReboot:
		return "reboot"
	case StartReasonThrowawayResume:
		return "throwaway_resume"
	default:
		return "unknown"
	}
}

// StartHook is an optional, edition-specific extension point invoked once a
// sandbox's identity and network slot are assigned, but before its guest
// begins executing. It keeps this package agnostic of edition-specific
// behavior at a lifecycle moment, rather than hardcoding it here. Unlike
// MapSubscriber, BeforeStart may perform local, bounded work — the caller
// enforces a fixed deadline regardless of ctx cancellation, and never delays
// or fails the sandbox's start if the hook errors, panics, or is still
// running past it.
type StartHook interface {
	// BeforeStart is called once per start attempt, after the sandbox is
	// registered in the network map and before its guest can execute for
	// this attempt. It gates that execution in real time — the caller waits
	// for it, up to a fixed deadline — but must not be relied upon to run to
	// completion: a slow, erroring, or hung implementation must not prevent
	// or delay the sandbox from starting beyond that deadline. Because the
	// caller does not, and cannot, cancel a BeforeStart call that is still
	// running when its deadline passes, an implementation that performs any
	// stateful or kernel-level mutation as part of this call is responsible
	// for fencing that mutation itself against having already been
	// superseded (e.g. by the same sandbox's own lifecycle having moved on,
	// or by its resources having been reassigned elsewhere) by the time the
	// mutation actually happens — infra provides no such guarantee on its
	// own.
	BeforeStart(ctx context.Context, sbx *Sandbox, reason StartReason) error
}

// NoopStartHook is a no-op StartHook, used when no edition-specific
// implementation is configured.
type NoopStartHook struct{}

var _ StartHook = NoopStartHook{}

func (NoopStartHook) BeforeStart(_ context.Context, _ *Sandbox, _ StartReason) error {
	return nil
}
