//go:build linux

package block

import (
	"bytes"
	"context"
	"fmt"
	"math/rand"
	"os"
	"slices"
	"testing"

	"github.com/RoaringBitmap/roaring/v2"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

// TestDedupFragmentationSim simulates pause/resume chains through the real
// dedup planner (dedupCompare) and the real header pipeline
// (DiffMetadata.ToDiffHeader merge/normalize), then estimates the
// cold-restore fetch cost of the resulting layouts by resolving a resumed
// working set through GetShiftedMapping — the same lookup restore uses.
//
// Model: hugepage-granular dirty tracking means one 4 KiB write dirties a
// whole 2 MiB block; dedup strips the unchanged pages, leaving references to
// frames of older layers at arbitrary packed offsets — fragmentation that
// compounds with chain depth. A cold resume materializes a working set of
// blocks, fetching one frame per distinct (layer, frame) reference; a
// hypothetical coalescing fetcher would merge adjacent frames of one layer
// into a single ranged read.
//
// Skipped unless DEDUP_SIM=1:
//
//	DEDUP_SIM=1 go test ./pkg/sandbox/block/ -run TestDedupFragmentationSim -v
func TestDedupFragmentationSim(t *testing.T) {
	t.Parallel()
	if os.Getenv("DEDUP_SIM") == "" {
		t.Skip("set DEDUP_SIM=1 to run the fragmentation simulation")
	}

	base := simConfig{
		blocks:       128, // 2 MiB blocks → 256 MiB memfile
		cycles:       12,
		hotRegions:   3,
		hotRegionBlk: 4,
		hotFrac:      0.6,
		scattered:    60,
		identFrac:    0.5,
		zeroFrac:     0.05,
		wsBlocks:     50, // ~100 MiB resumed working set
		globalBudget: 256,
		workloadSeed: 42,
		templateSeed: 7,
		wsSeed:       99,
	}

	t.Logf("memfile %d MiB, %d cycles, working set %d blocks (%d MiB); fetch model: %.0fms TTFB, %.0f MB/s/stream, %.0fx concurrency",
		base.blocks*2, base.cycles, base.wsBlocks, base.wsBlocks*2, simTTFBMs, simBWMBps, simConcurrency)

	t.Logf("== scenarios at base conditions ==")
	logSimHeader(t)
	for _, s := range simScenarios(base) {
		logSimRow(t, runSimChain(t, base, s))
	}

	t.Logf("== per-frame price K x assumed block fault pct (0 = strict attribution) ==")
	logSimHeader(t)
	for _, budget := range []int{16, 64, 256, 1024} {
		for _, pct := range []int{0, 20, 40, 80} {
			cfg := base
			cfg.globalBudget = budget
			s := simScenario{
				name:    fmt.Sprintf("K=%d p=%d", budget, pct),
				planner: plannerDedup(DedupBudget{MaxPagesPerPromotedFrame: budget, BlockFaultPct: pct}),
			}
			logSimRow(t, runSimChain(t, cfg, s))
		}
	}

	t.Logf("== workload sweep: scattered writes per cycle x chain depth ==")
	logSimHeader(t)
	for _, scattered := range []int{20, 60, 200} {
		for _, cycles := range []int{12, 24} {
			cfg := base
			cfg.scattered = scattered
			cfg.cycles = cycles
			for _, s := range []simScenario{
				{name: fmt.Sprintf("s=%d c=%d dedup", scattered, cycles), planner: plannerDedup(DedupBudget{})},
				{name: fmt.Sprintf("s=%d c=%d +global", scattered, cycles), planner: plannerDedup(DedupBudget{MaxPagesPerPromotedFrame: cfg.globalBudget, BlockFaultPct: 40})},
			} {
				logSimRow(t, runSimChain(t, cfg, s))
			}
		}
	}
}

// Cold-fetch cost model (illustrative): per-fetch first-byte latency and
// per-stream bandwidth, fanned out over concurrent streams.
const (
	simTTFBMs      = 25.0
	simBWMBps      = 150.0
	simConcurrency = 16.0

	simBlockSize = int64(2 << 20)
	simPageSize  = int64(header.PageSize)
)

type simConfig struct {
	blocks       int
	cycles       int
	hotRegions   int
	hotRegionBlk int
	hotFrac      float64
	scattered    int
	identFrac    float64
	zeroFrac     float64
	wsBlocks     int
	globalBudget int
	workloadSeed int64
	templateSeed int64
	wsSeed       int64
}

func (c simConfig) totalPages() int  { return c.blocks * int(simBlockSize/simPageSize) }
func (c simConfig) size() int64      { return int64(c.blocks) * simBlockSize }
func (c simConfig) pagesPerBlk() int { return int(simBlockSize / simPageSize) }

type simWrite struct {
	page    int
	newData bool // false: page written with identical content
	zero    bool
	seed    int64
}

func simWorkload(cfg simConfig) [][]simWrite {
	rng := rand.New(rand.NewSource(cfg.workloadSeed))
	workload := make([][]simWrite, cfg.cycles)
	for c := range workload {
		var ws []simWrite
		for range cfg.hotRegions {
			startBlock := rng.Intn(cfg.blocks - cfg.hotRegionBlk)
			for p := startBlock * cfg.pagesPerBlk(); p < (startBlock+cfg.hotRegionBlk)*cfg.pagesPerBlk(); p++ {
				if rng.Float64() < cfg.hotFrac {
					ws = append(ws, simWrite{page: p, newData: true, zero: rng.Float64() < cfg.zeroFrac, seed: rng.Int63()})
				}
			}
		}
		for range cfg.scattered {
			ws = append(ws, simWrite{
				page:    rng.Intn(cfg.totalPages()),
				newData: rng.Float64() >= cfg.identFrac,
				zero:    rng.Float64() < cfg.zeroFrac,
				seed:    rng.Int63(),
			})
		}
		workload[c] = ws
	}

	return workload
}

// simPlanner produces the diff plan for one pause: the pages stored in the
// new layer and the pages that became empty. flatten requests a full
// store-everything snapshot for this cycle (chain reset).
type simPlanner func(t *testing.T, mem, parent []byte, hdr *header.Header, dirty *roaring.Bitmap, cycle int) (*header.DiffMetadata, *dedupPlan)

func planFromDedup(plan *dedupPlan) *header.DiffMetadata {
	return &header.DiffMetadata{Dirty: plan.pageDirty, Empty: plan.pageEmpty, BlockSize: simPageSize}
}

func plannerDedup(budget DedupBudget) simPlanner {
	return func(t *testing.T, mem, parent []byte, hdr *header.Header, dirty *roaring.Bitmap, _ int) (*header.DiffMetadata, *dedupPlan) {
		t.Helper()
		src := func(absOff int64) ([]byte, error) { return mem[absOff : absOff+simBlockSize], nil }
		base := &fakeOriginalDevice{data: parent, hdr: hdr}
		plan, err := dedupCompare(context.Background(), src, base, dirty, simBlockSize, false, budget)
		require.NoError(t, err)

		return planFromDedup(plan), plan
	}
}

// plannerNoDedup stores every non-zero page of every dirty block.
func plannerNoDedup() simPlanner {
	return func(t *testing.T, mem, _ []byte, _ *header.Header, dirty *roaring.Bitmap, _ int) (*header.DiffMetadata, *dedupPlan) {
		t.Helper()

		return classifyAll(mem, dirty), nil
	}
}

func classifyAll(mem []byte, dirty *roaring.Bitmap) *header.DiffMetadata {
	meta := &header.DiffMetadata{Dirty: roaring.New(), Empty: roaring.New(), BlockSize: simPageSize}
	for r := range BitsetRanges(dirty, simBlockSize) {
		for p := r.Start / simPageSize; p < r.End()/simPageSize; p++ {
			if header.IsZero(mem[p*simPageSize : (p+1)*simPageSize]) {
				meta.Empty.Add(uint32(p))
			} else {
				meta.Dirty.Add(uint32(p))
			}
		}
	}

	return meta
}

// plannerFlatten runs the inner planner, but every n-th pause stores the
// whole memfile (LSM-style chain compaction). The schedule is offset so a
// flatten never coincides with the final pause, which would make the resume
// artificially perfect.
func plannerFlatten(n int, inner simPlanner) simPlanner {
	return func(t *testing.T, mem, parent []byte, hdr *header.Header, dirty *roaring.Bitmap, cycle int) (*header.DiffMetadata, *dedupPlan) {
		t.Helper()
		if cycle > 0 && cycle%n == 0 {
			all := roaring.New()
			all.AddRange(0, uint64(int64(len(mem))/simBlockSize))

			return classifyAll(mem, all), nil
		}

		return inner(t, mem, parent, hdr, dirty, cycle)
	}
}

// plannerRunKnapsack is an alternative global pass evaluated for a future
// coalescing fetcher: knapsack items are maximal runs of adjacent referenced
// frames within one layer (one coalesced ranged read each) instead of single
// frames. Promotion is applied on top of a budget-less dedup plan.
func plannerRunKnapsack(budgetPages int) simPlanner {
	plain := plannerDedup(DedupBudget{})

	return func(t *testing.T, mem, parent []byte, hdr *header.Header, dirty *roaring.Bitmap, cycle int) (*header.DiffMetadata, *dedupPlan) {
		t.Helper()
		meta, plan := plain(t, mem, parent, hdr, dirty, cycle)

		type frameKey struct {
			build uuid.UUID
			frame uint64
		}
		pagesByKey := map[frameKey]*roaring.Bitmap{}
		for r := range BitsetRanges(dirty, simBlockSize) {
			for off := r.Start; off < r.End(); off += simPageSize {
				idx := uint32(off / simPageSize)
				if plan.pageDirty.Contains(idx) || plan.pageEmpty.Contains(idx) {
					continue
				}
				m, err := hdr.GetShiftedMapping(context.Background(), off)
				if err != nil || m.BuildId == uuid.Nil {
					continue
				}
				k := frameKey{m.BuildId, m.Offset / uint64(simBlockSize)}
				if pagesByKey[k] == nil {
					pagesByKey[k] = roaring.New()
				}
				pagesByKey[k].Add(idx)
			}
		}

		framesByBuild := map[uuid.UUID][]uint64{}
		for k := range pagesByKey {
			framesByBuild[k.build] = append(framesByBuild[k.build], k.frame)
		}
		type run struct {
			pages  *roaring.Bitmap
			frames int
		}
		var runs []run
		for b, frames := range framesByBuild {
			slices.Sort(frames)
			for i := 0; i < len(frames); {
				j := i + 1
				for j < len(frames) && frames[j] == frames[j-1]+1 {
					j++
				}
				r := run{pages: roaring.New(), frames: j - i}
				for _, f := range frames[i:j] {
					r.pages.Or(pagesByKey[frameKey{b, f}])
				}
				runs = append(runs, r)
				i = j
			}
		}
		slices.SortStableFunc(runs, func(a, b run) int {
			return int(a.pages.GetCardinality()) - int(b.pages.GetCardinality())
		})

		windowPages := int(simBlockSize / simPageSize)
		spent := 0
		for _, r := range runs {
			n := int(r.pages.GetCardinality())
			if spent+n > budgetPages {
				break // sorted by page count, so no later run fits either
			}
			if n >= r.frames*windowPages {
				continue // promoting would add as many diff frames as it removes
			}
			spent += n
			plan.pageDirty.Or(r.pages)
		}

		return meta, plan
	}
}

type resumeStat struct {
	fetches   int // distinct (layer, frame) reads, today's fetcher
	coalesced int // ranged reads if adjacent frames of a layer were merged
	layers    int
	mib       float64
	estMs     float64
	maxFanout int // worst per-block frame fan-out (single-fault latency)
}

func simResume(t *testing.T, hdr *header.Header, wsBlocks []int) resumeStat {
	t.Helper()
	type frameKey struct {
		build uuid.UUID
		frame uint64
	}
	frames := make(map[frameKey]struct{})
	layers := make(map[uuid.UUID]struct{})
	maxFanout := 0
	for _, b := range wsBlocks {
		blockFrames := make(map[frameKey]struct{})
		for p := int64(b) * simBlockSize / simPageSize; p < int64(b+1)*simBlockSize/simPageSize; p++ {
			m, err := hdr.GetShiftedMapping(context.Background(), p*simPageSize)
			require.NoError(t, err)
			if m.BuildId == uuid.Nil {
				continue
			}
			k := frameKey{m.BuildId, m.Offset / uint64(simBlockSize)}
			frames[k] = struct{}{}
			blockFrames[k] = struct{}{}
			layers[m.BuildId] = struct{}{}
		}
		maxFanout = max(maxFanout, len(blockFrames))
	}

	framesByBuild := map[uuid.UUID][]uint64{}
	for k := range frames {
		framesByBuild[k.build] = append(framesByBuild[k.build], k.frame)
	}
	coalesced := 0
	for _, fs := range framesByBuild {
		slices.Sort(fs)
		for i := 0; i < len(fs); {
			j := i + 1
			for j < len(fs) && fs[j] == fs[j-1]+1 {
				j++
			}
			coalesced++
			i = j
		}
	}

	mib := float64(len(frames)) * 2

	return resumeStat{
		fetches:   len(frames),
		coalesced: coalesced,
		layers:    len(layers),
		mib:       mib,
		estMs:     (float64(len(frames))*simTTFBMs + mib/simBWMBps*1000) / simConcurrency,
		maxFanout: maxFanout,
	}
}

type simScenario struct {
	name    string
	planner simPlanner
}

func simScenarios(cfg simConfig) []simScenario {
	return []simScenario{
		{"no-dedup", plannerNoDedup()},
		{"dedup", plannerDedup(DedupBudget{})},
		{"dedup+global", plannerDedup(DedupBudget{MaxPagesPerPromotedFrame: cfg.globalBudget, BlockFaultPct: 40})},
		{"dedup+global+block", plannerDedup(DedupBudget{
			MaxPagesPerPromotedFrame:       cfg.globalBudget,
			BlockFaultPct:                  40,
			MaxFetchWindowsPerBlock:        8,
			MaxPromotedParentPagesPerBlock: 64,
		})},
		{"dedup+runKnapsack", plannerRunKnapsack(cfg.globalBudget)},
		{"dedup+global+flat6", plannerFlatten(6, plannerDedup(DedupBudget{MaxPagesPerPromotedFrame: cfg.globalBudget, BlockFaultPct: 40}))},
	}
}

type simResult struct {
	name      string
	storedMiB float64
	hot       resumeStat
}

func runSimChain(t *testing.T, cfg simConfig, s simScenario) simResult {
	t.Helper()

	// Template: identity-packed layer with a sprinkle of zero pages.
	trng := rand.New(rand.NewSource(cfg.templateSeed))
	mem := make([]byte, cfg.size())
	_, err := trng.Read(mem)
	require.NoError(t, err)
	for p := range cfg.totalPages() {
		if trng.Float64() < cfg.zeroFrac {
			clear(mem[int64(p)*simPageSize : int64(p+1)*simPageSize])
		}
	}
	parent := append([]byte(nil), mem...)

	templateBuild := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	hdr, err := header.NewHeader(header.NewTemplateMetadata(templateBuild, uint64(simPageSize), uint64(cfg.size())), nil)
	require.NoError(t, err)

	workload := simWorkload(cfg)
	var storedPages int64
	for cycle, ws := range workload {
		dirty := roaring.New()
		for _, w := range ws {
			if w.newData {
				pg := mem[int64(w.page)*simPageSize : int64(w.page+1)*simPageSize]
				if w.zero {
					clear(pg)
				} else {
					_, err := rand.New(rand.NewSource(w.seed)).Read(pg)
					require.NoError(t, err)
				}
			}
			dirty.Add(uint32(int64(w.page) * simPageSize / simBlockSize))
		}

		meta, _ := s.planner(t, mem, parent, hdr, dirty, cycle)
		storedPages += int64(meta.Dirty.GetCardinality())

		hdr, err = meta.ToDiffHeader(context.Background(), hdr, uuid.New())
		require.NoError(t, err)
		copy(parent, mem)
	}

	// The working set derives from the workload only, so every scenario
	// resumes the exact same blocks: half the last cycle's writes (hot),
	// half random cold blocks.
	var hot []int
	for _, w := range workload[cfg.cycles-1] {
		b := int(int64(w.page) * simPageSize / simBlockSize)
		if !slices.Contains(hot, b) {
			hot = append(hot, b)
		}
	}
	slices.Sort(hot)
	if len(hot) > cfg.wsBlocks/2 {
		hot = hot[:cfg.wsBlocks/2]
	}
	seen := map[int]struct{}{}
	for _, b := range hot {
		seen[b] = struct{}{}
	}
	wsBlocks := hot
	wrng := rand.New(rand.NewSource(cfg.wsSeed))
	for len(wsBlocks) < cfg.wsBlocks {
		b := wrng.Intn(cfg.blocks)
		if _, ok := seen[b]; !ok {
			seen[b] = struct{}{}
			wsBlocks = append(wsBlocks, b)
		}
	}

	return simResult{
		name:      s.name,
		storedMiB: float64(storedPages) * float64(simPageSize) / (1 << 20),
		hot:       simResume(t, hdr, wsBlocks),
	}
}

func logSimHeader(t *testing.T) {
	t.Helper()
	t.Logf("%-22s %10s %8s %10s %8s %8s %7s %10s", "scenario", "storedMiB", "fetches", "coalesced", "layers", "fetchMiB", "estMs", "maxFanout")
}

func logSimRow(t *testing.T, r simResult) {
	t.Helper()
	t.Logf("%-22s %10.1f %8d %10d %8d %8.0f %7.0f %10d",
		r.name, r.storedMiB, r.hot.fetches, r.hot.coalesced, r.hot.layers, r.hot.mib, r.hot.estMs, r.hot.maxFanout)
}

// TestDedupRandomChain_NoCorruption runs random pause chains with random
// budget combinations through the real planner and header merge, keeping a
// per-layer store packed exactly like export packs the diff (Dirty pages
// ascending). After every pause each page of the memfile is resolved through
// GetShiftedMapping — the lookup restore uses — and must be byte-identical to
// live memory, so any misclassification, bad promotion, or packing/merge
// offset bug fails here. Runs in CI (seeds are fixed, ~seconds).
func TestDedupRandomChain_NoCorruption(t *testing.T) {
	t.Parallel()

	for seed := range int64(6) {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			t.Parallel()
			rng := rand.New(rand.NewSource(seed))

			cfg := simConfig{
				blocks:       16,
				cycles:       6,
				hotRegions:   2,
				hotRegionBlk: 2,
				hotFrac:      0.6,
				scattered:    40,
				identFrac:    0.5,
				zeroFrac:     0.1,
				workloadSeed: seed,
				templateSeed: seed + 100,
			}
			budget := DedupBudget{FetchRunWindowPages: []int{0, 16, 512}[rng.Intn(3)]}
			if seed&1 != 0 {
				budget.MaxFetchWindowsPerBlock = 1 + rng.Intn(4)
				budget.MaxPromotedParentPagesPerBlock = 1 << rng.Intn(7)
			}
			if seed&2 != 0 {
				budget.MaxPagesPerPromotedFrame = 1 << (4 + rng.Intn(6))
				budget.BlockFaultPct = []int{0, 20, 40, 80}[rng.Intn(4)]
			}
			t.Logf("budget: %+v", budget)

			trng := rand.New(rand.NewSource(cfg.templateSeed))
			mem := make([]byte, cfg.size())
			_, err := trng.Read(mem)
			require.NoError(t, err)
			for p := range cfg.totalPages() {
				if trng.Float64() < cfg.zeroFrac {
					clear(mem[int64(p)*simPageSize : int64(p+1)*simPageSize])
				}
			}
			parent := slices.Clone(mem)

			templateBuild := uuid.New()
			hdr, err := header.NewHeader(header.NewTemplateMetadata(templateBuild, uint64(simPageSize), uint64(cfg.size())), nil)
			require.NoError(t, err)
			layers := map[uuid.UUID][]byte{templateBuild: slices.Clone(mem)}

			planner := plannerDedup(budget)
			for cycle, ws := range simWorkload(cfg) {
				dirty := roaring.New()
				for _, w := range ws {
					if w.newData {
						pg := mem[int64(w.page)*simPageSize : int64(w.page+1)*simPageSize]
						if w.zero {
							clear(pg)
						} else {
							_, err := rand.New(rand.NewSource(w.seed)).Read(pg)
							require.NoError(t, err)
						}
					}
					dirty.Add(uint32(int64(w.page) * simPageSize / simBlockSize))
				}

				meta, _ := planner(t, mem, parent, hdr, dirty, cycle)

				var payload []byte
				for it := meta.Dirty.Iterator(); it.HasNext(); {
					p := int64(it.Next())
					payload = append(payload, mem[p*simPageSize:(p+1)*simPageSize]...)
				}
				build := uuid.New()
				layers[build] = payload

				hdr, err = meta.ToDiffHeader(context.Background(), hdr, build)
				require.NoError(t, err)
				copy(parent, mem)

				for off := int64(0); off < cfg.size(); off += simPageSize {
					page := off / simPageSize
					m, err := hdr.GetShiftedMapping(context.Background(), off)
					require.NoError(t, err)
					want := mem[off : off+simPageSize]
					if m.BuildId == uuid.Nil {
						require.True(t, header.IsZero(want), "cycle %d: page %d mapped empty but live page is non-zero", cycle, page)

						continue
					}
					layer, ok := layers[m.BuildId]
					require.True(t, ok, "cycle %d: page %d mapped to unknown build %s", cycle, page, m.BuildId)
					start := int64(m.Offset)
					require.LessOrEqual(t, start+simPageSize, int64(len(layer)), "cycle %d: page %d mapped past layer end", cycle, page)
					require.True(t, bytes.Equal(want, layer[start:start+simPageSize]), "cycle %d: page %d restored bytes differ", cycle, page)
				}
			}
		})
	}
}
