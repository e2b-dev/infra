//go:build linux

package sandbox

import (
	"slices"

	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/template/metadata"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
	"github.com/e2b-dev/infra/packages/shared/pkg/units"
)

// resume-prefetch-source flag values (see featureflags.ResumePrefetchSourceFlag).
const (
	resumePrefetchOff       = "off"
	resumePrefetchInit      = "init"
	resumePrefetchLastCycle = "last-cycle"
	resumePrefetchBoth      = "both"
)

// selectResumePrefetch maps the resume-prefetch-source flag value to which
// traces the resume prefetcher replays. "init" (and any unknown value) keeps
// today's behavior — replay only the build-time/harvested init trace — so the
// default is a no-op-equivalent.
func selectResumePrefetch(source string) (useInit, useLastCycle bool) {
	switch source {
	case resumePrefetchOff:
		return false, false
	case resumePrefetchLastCycle:
		return false, true
	case resumePrefetchBoth:
		return true, true
	default: // resumePrefetchInit and any unrecognized value
		return true, false
	}
}

// capResumePrefetch bounds a prefetch mapping to at most maxMiB of blocks,
// keeping the earliest (offset-order) blocks and leaving the rest to
// demand-fault. maxMiB < 0 means uncapped; m is returned unchanged when it
// already fits, the cap is disabled, or the block size is unknown. A nil
// mapping returns nil.
func capResumePrefetch(m *metadata.MemoryPrefetchMapping, maxMiB int) *metadata.MemoryPrefetchMapping {
	if m == nil || maxMiB < 0 || m.BlockSize <= 0 {
		return m
	}

	maxBlocks := units.MBToBytes(int64(maxMiB)) / m.BlockSize
	if int64(len(m.Indices)) <= maxBlocks {
		return m
	}

	return &metadata.MemoryPrefetchMapping{
		Indices:   m.Indices[:maxBlocks],
		BlockSize: m.BlockSize,
	}
}

// buildDiffMemoryPrefetchMapping builds a prefetch mapping over only the
// blocks owned by this snapshot's own pause diff (BuildMap.BuildId ==
// Metadata.BuildId) — i.e. the pages the last resume→pause cycle actually
// wrote (the sandbox's "last-cycle" working set).
//
// Pause/resume carries no build-time prefetch mapping (SameVersionTemplate
// drops it on the resumed snapshot's metadata), so today it demand-faults
// everything cold. But the pages a resume→pause cycle wrote are already
// recorded: they ARE the pause diff, present in the merged memfile header as
// the BuildMap entries whose BuildId equals this header's own build ID. No
// separate dirty-bitmap capture at pause time is needed — deriving the
// mapping from the header at resume is sufficient and reuses data that
// already exists.
//
// Blocks are enumerated at the memfile's block size (the prefetcher's fetch
// unit) and returned in offset order — readahead- and coalescing-friendly,
// and there is no access-order predictor for pause/resume anyway.
//
// Returns nil if the diff is empty, the header/metadata isn't usable, or the
// header has no base layer. The last guard matters: a base / build template
// (as opposed to a paused snapshot) maps its entire memfile to its own
// BuildId, so without it a create/build resume with resume-prefetch-source
// including last-cycle would prefetch the whole template image, not a diff.
func buildDiffMemoryPrefetchMapping(h *header.Header) *metadata.MemoryPrefetchMapping {
	if h == nil || h.Metadata == nil || h.Metadata.BlockSize == 0 {
		return nil
	}

	blockSize := int64(h.Metadata.BlockSize)
	own := h.Metadata.BuildId

	// A merged/dedup memfile header has overlapping BuildMap entries across
	// layers (precedence is resolved at read time), so the same block offset
	// can appear in multiple entries. Dedup by block index: fetch each block
	// once (source.Slice resolves the top layer itself). Offsets/lengths are
	// uint64; walk in uint64 to avoid an int64 cast that could overflow.
	uBlockSize := uint64(blockSize)
	seen := make(map[uint64]struct{})
	hasBase := false
	for _, bm := range h.Mapping.All() {
		if bm.BuildId == uuid.Nil {
			continue
		}
		if bm.BuildId != own {
			hasBase = true // a distinct base/parent layer exists

			continue
		}
		if bm.Length == 0 {
			continue
		}

		// Enumerate by block index, not by byte stride. A merged/dedup entry
		// can start unaligned to the block size (dedup emits PageSize-granular
		// entries, and NormalizeMappings merges adjacent same-build runs), so
		// stepping by uBlockSize from an unaligned Offset would skip the final
		// block whenever the entry straddles a block boundary.
		firstBlock := bm.Offset / uBlockSize
		lastBlock := (bm.Offset + bm.Length - 1) / uBlockSize
		for idx := firstBlock; idx <= lastBlock; idx++ {
			seen[idx] = struct{}{}
		}
	}

	// A header that maps every block to its own BuildId has no base layer — it
	// is a full image (a base / build template), not a pause diff. Returning
	// its "own" blocks would prefetch the entire template on a normal
	// create/build resume, so only treat own-BuildId blocks as a last-cycle
	// diff when the snapshot is layered on a distinct base.
	if !hasBase || len(seen) == 0 {
		return nil
	}

	indices := make([]uint64, 0, len(seen))
	for idx := range seen {
		indices = append(indices, idx)
	}
	slices.Sort(indices)

	return &metadata.MemoryPrefetchMapping{
		Indices:   indices,
		BlockSize: blockSize,
	}
}
