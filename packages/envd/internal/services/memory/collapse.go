// Package memory implements best-effort consolidation of envd's own anonymous
// heap before a snapshot. envd's Go heap arenas accumulate live pages scattered
// across many distinct 2 MiB guest-physical frames over the sandbox's life; on a
// cold resume each such frame is a separate serial fault from remote storage, so
// envd-init touching its scattered working set dominates resume latency.
// Collapsing the heap into 2 MiB transparent hugepages before pause migrates
// those pages together, so on resume envd touches far fewer frames.
package memory

// Stats summarizes a CollapseSelf run.
//
// MADV_COLLAPSE returns success both when it migrates scattered base pages into
// a new hugepage and when the window was already a hugepage (a no-op), so the
// raw success count overstates real work. We split it with the process's
// AnonHugePages delta: Collapsed counts windows actually migrated this run,
// AlreadyHuge counts windows that were already huge. Invariant:
// Chunks = Collapsed + AlreadyHuge + Skipped.
type Stats struct {
	Regions     int // anonymous read-write regions scanned
	Chunks      int // 2 MiB chunks attempted = Collapsed + AlreadyHuge + Skipped
	Collapsed   int // chunks whose base pages were actually migrated into a new hugepage (real work)
	AlreadyHuge int // chunks MADV_COLLAPSE accepted but were already hugepages (no work)
	Skipped     int // chunks that could not be collapsed (empty or ineligible)
}
