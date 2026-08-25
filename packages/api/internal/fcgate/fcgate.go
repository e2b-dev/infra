// Package fcgate holds the API-side Firecracker version gates for
// version-gated sandbox features. The gate core (parse the version, check the
// feature's release floor) lives here exactly once; the failure ACTIONS stay
// with the callers, because they genuinely differ — the create handler
// refuses with a 400, the pause handler with a 409, the timeout evictor
// degrades to a memory snapshot.
//
// Exact vs approximate is EXPLICIT, never inferred from the version string:
//
//   - Sandbox records whose FirecrackerVersionResolved flag is set carry the
//     orchestrator-resolved version the sandbox actually runs (frozen at
//     start, echoed on the create response). Gates check those with
//     SupportsFilesystemSnapshots — exact, no flag consultation, so the
//     answer can never disagree with the orchestrator's own gate.
//
//   - The create-time check runs before any record exists and sees only the
//     build's DECLARED version. SupportsFilesystemSnapshotsDeclared resolves
//     it once through the current flag — mirroring what the orchestrator will
//     do at start — and checks the result. The approximation can disagree
//     with the eventual frozen value under LD context skew or a flag change,
//     but no VM exists yet, so a wrong answer costs a spurious 400 or a
//     policy the timeout eviction later degrades — never a stranded VM.
//
//   - Records that predate the resolved flag are only approximations of the
//     running binary. The live-VM call sites (pause, evictor) still check
//     them exactly rather than re-resolving: re-resolution could turn a
//     non-qualifying record into a qualifying answer under a standing flag
//     pin or context skew, and a false-allow there commits the pause chain
//     into the orchestrator's post-teardown refusal — a stranded VM. The
//     residual, documented bound: a pre-field record whose stored version
//     qualifies while the running binary does not, which requires an e2b
//     line pinned to a non-qualifying binary by the flag; this population
//     drains with the rollout window.
package fcgate

import (
	"context"

	"github.com/e2b-dev/infra/packages/shared/pkg/fcversion"
	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
)

// SupportsFilesystemSnapshots reports whether the given Firecracker version
// carries filesystem-only snapshot support. Exact: no flag consultation —
// use for versions that already are (or best approximate) the running
// binary. Unparsable versions fail closed.
func SupportsFilesystemSnapshots(version string) bool {
	info, err := fcversion.New(version)

	return err == nil && info.HasFilesystemSnapshots()
}

// SupportsFilesystemSnapshotsDeclared reports whether a DECLARED build
// version resolves to a Firecracker release with filesystem-only snapshot
// support: it resolves once through the current firecracker-versions flag —
// mirroring the orchestrator's start-time resolution — and checks the
// result. Only for call sites where no VM exists yet (create): the
// approximation must never gate a live sandbox's pause chain. Unparsable
// resolutions fail closed.
func SupportsFilesystemSnapshotsDeclared(ctx context.Context, flags *featureflags.Client, declared string) bool {
	return SupportsFilesystemSnapshots(featureflags.ResolveFirecrackerVersion(ctx, flags, declared))
}
