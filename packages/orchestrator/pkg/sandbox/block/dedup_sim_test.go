//go:build linux

package block

import (
	"context"
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
// dedup planner (dedupCompare) and estimates the cold-restore fetch cost of
// the resulting layer layout. It compares storing whole dirty blocks
// (no dedup), plain dedup, and dedup with the promotion budgets.
//
// Model: hugepage-granular dirty tracking means one 4 KiB write dirties a
// whole 2 MiB block; dedup strips the unchanged pages, leaving references to
// frames of older layers at arbitrary packed offsets — fragmentation that
// compounds with chain depth. A cold resume materializes a working set of
// blocks, fetching one frame per distinct (layer, frame) reference.
//
// Skipped unless DEDUP_SIM=1:
//
//	DEDUP_SIM=1 go test ./pkg/sandbox/block/ -run TestDedupFragmentationSim -v
func TestDedupFragmentationSim(t *testing.T) {
	t.Parallel()
	if os.Getenv("DEDUP_SIM") == "" {
		t.Skip("set DEDUP_SIM=1 to run the fragmentation simulation")
	}

	const (
		simBlocks = 128 // 2 MiB blocks → 256 MiB memfile
		simCycles = 12  // pause/resume cycles

		// Per cycle: a few dense hot regions plus envd-style scattered
		// single-page writes, half of which rewrite identical content.
		simHotRegions             = 3
		simHotRegionBlocks        = 4
		simHotRewriteFrac         = 0.6
		simScatteredWrites        = 60
		simScatteredIdenticalFrac = 0.5
		simZeroWriteFrac          = 0.05

		simWorkingSetBlocks = 50 // ~100 MiB resumed working set

		// Cold-fetch cost model (illustrative): per-fetch first-byte latency
		// and per-stream bandwidth, fanned out over concurrent streams.
		simTTFBMs       = 25.0
		simBWMBps       = 150.0
		simConcurrency  = 16.0
		simFrameMiB     = 2.0
		simTemplateZero = 0.05 // fraction of template pages that are zero
	)

	blockSize := int64(2 << 20)
	pageSize := int64(header.PageSize)
	pagesPerBlock := int(blockSize / pageSize)
	totalPages := simBlocks * pagesPerBlock
	size := int64(simBlocks) * blockSize

	type simWrite struct {
		page    int
		newData bool // false: page written with identical content
		zero    bool
		seed    int64
	}

	// Workload is generated once and replayed for every scenario.
	wrng := rand.New(rand.NewSource(42))
	workload := make([][]simWrite, simCycles)
	for c := range workload {
		var ws []simWrite
		for range simHotRegions {
			startBlock := wrng.Intn(simBlocks - simHotRegionBlocks)
			for p := startBlock * pagesPerBlock; p < (startBlock+simHotRegionBlocks)*pagesPerBlock; p++ {
				if wrng.Float64() < simHotRewriteFrac {
					ws = append(ws, simWrite{page: p, newData: true, zero: wrng.Float64() < simZeroWriteFrac, seed: wrng.Int63()})
				}
			}
		}
		for range simScatteredWrites {
			ws = append(ws, simWrite{
				page:    wrng.Intn(totalPages),
				newData: wrng.Float64() >= simScatteredIdenticalFrac,
				zero:    wrng.Float64() < simZeroWriteFrac,
				seed:    wrng.Int63(),
			})
		}
		workload[c] = ws
	}

	// pageRef is where a page's data lives: a packed offset in one layer's
	// stored file, or nowhere (zero page).
	type pageRef struct {
		build uuid.UUID
		off   uint64
		empty bool
	}

	templateBuild := uuid.MustParse("00000000-0000-0000-0000-000000000001")

	newTemplate := func() ([]byte, []pageRef) {
		trng := rand.New(rand.NewSource(7))
		mem := make([]byte, size)
		_, err := trng.Read(mem)
		require.NoError(t, err)
		table := make([]pageRef, totalPages)
		for p := range table {
			if trng.Float64() < simTemplateZero {
				clear(mem[int64(p)*pageSize : int64(p+1)*pageSize])
				table[p] = pageRef{empty: true}
			} else {
				// Template layout is identity-packed: guest offset == storage offset.
				table[p] = pageRef{build: templateBuild, off: uint64(p) * uint64(pageSize)}
			}
		}

		return mem, table
	}

	buildHeader := func(build uuid.UUID, table []pageRef) *header.Header {
		var maps []header.BuildMap
		for p := 0; p < len(table); {
			q := p + 1
			for q < len(table) {
				a, b := table[q-1], table[q]
				contiguous := (a.empty && b.empty) ||
					(!a.empty && !b.empty && a.build == b.build && b.off == a.off+uint64(pageSize))
				if !contiguous {
					break
				}
				q++
			}
			m := header.BuildMap{
				Offset:  uint64(p) * uint64(pageSize),
				Length:  uint64(q-p) * uint64(pageSize),
				BuildId: uuid.Nil,
			}
			if !table[p].empty {
				m.BuildId = table[p].build
				m.BuildStorageOffset = table[p].off
			}
			maps = append(maps, m)
			p = q
		}
		hdr, err := header.NewHeader(header.NewTemplateMetadata(build, uint64(pageSize), uint64(size)), maps)
		require.NoError(t, err)

		return hdr
	}

	applyWrites := func(mem []byte, ws []simWrite) *roaring.Bitmap {
		dirty := roaring.New()
		for _, w := range ws {
			if w.newData {
				pg := mem[int64(w.page)*pageSize : int64(w.page+1)*pageSize]
				if w.zero {
					clear(pg)
				} else {
					_, err := rand.New(rand.NewSource(w.seed)).Read(pg)
					require.NoError(t, err)
				}
			}
			dirty.Add(uint32(int64(w.page) * pageSize / blockSize))
		}

		return dirty
	}

	type resumeStat struct {
		fetches int
		layers  int
		mib     float64
		estMs   float64
		// maxFanout is the worst per-block frame fan-out: the fetches a
		// single cold 2 MiB hugepage fault must wait for. This is what the
		// per-block window cap bounds; totals are what the global budget
		// minimizes.
		maxFanout int
	}

	resume := func(table []pageRef, wsBlocks []int) resumeStat {
		type frameKey struct {
			build uuid.UUID
			frame uint64
		}
		frames := make(map[frameKey]struct{})
		layers := make(map[uuid.UUID]struct{})
		maxFanout := 0
		for _, b := range wsBlocks {
			blockFrames := make(map[frameKey]struct{})
			for p := b * pagesPerBlock; p < (b+1)*pagesPerBlock; p++ {
				if table[p].empty {
					continue
				}
				k := frameKey{table[p].build, table[p].off / uint64(blockSize)}
				frames[k] = struct{}{}
				blockFrames[k] = struct{}{}
				layers[table[p].build] = struct{}{}
			}
			maxFanout = max(maxFanout, len(blockFrames))
		}
		mib := float64(len(frames)) * simFrameMiB

		return resumeStat{
			fetches:   len(frames),
			layers:    len(layers),
			mib:       mib,
			estMs:     (float64(len(frames))*simTTFBMs + mib/simBWMBps*1000) / simConcurrency,
			maxFanout: maxFanout,
		}
	}

	// Working sets are derived from the workload only, so every scenario
	// resumes the exact same blocks.
	wsHot := func() []int {
		seen := map[int]struct{}{}
		var blocks []int
		for _, w := range workload[simCycles-1] {
			b := int(int64(w.page) * pageSize / blockSize)
			if _, ok := seen[b]; !ok {
				seen[b] = struct{}{}
				blocks = append(blocks, b)
			}
		}
		slices.Sort(blocks)
		rng := rand.New(rand.NewSource(99))
		for len(blocks) < simWorkingSetBlocks {
			b := rng.Intn(simBlocks)
			if _, ok := seen[b]; !ok {
				seen[b] = struct{}{}
				blocks = append(blocks, b)
			}
		}

		return blocks[:simWorkingSetBlocks]
	}()

	type result struct {
		name           string
		storedMiB      float64
		parentFrames   int64
		promotedFrames int64
		promotedPages  int64
		hot            resumeStat
	}

	run := func(name string, dedup bool, budget DedupBudget) result {
		mem, table := newTemplate()
		parent := append([]byte(nil), mem...)
		hdr := buildHeader(templateBuild, table)

		var storedPages, parentFrames, promotedFrames, promotedPages int64
		for _, ws := range workload {
			dirty := applyWrites(mem, ws)
			layerBuild := uuid.New()

			if dedup {
				src := func(absOff int64) ([]byte, error) { return mem[absOff : absOff+blockSize], nil }
				base := &fakeOriginalDevice{data: parent, hdr: hdr}
				plan, err := dedupCompare(context.Background(), src, base, dirty, blockSize, false, budget)
				require.NoError(t, err)

				slot := uint64(0)
				it := plan.pageDirty.Iterator()
				for it.HasNext() {
					table[it.Next()] = pageRef{build: layerBuild, off: slot * uint64(pageSize)}
					slot++
				}
				ie := plan.pageEmpty.Iterator()
				for ie.HasNext() {
					table[ie.Next()] = pageRef{empty: true}
				}
				storedPages += int64(plan.pageDirty.GetCardinality())
				parentFrames, promotedFrames, promotedPages = plan.parentFrames, plan.promotedFrames, plan.promotedPages+plan.promotedFramePages
			} else {
				// No dedup: store every non-zero page of every dirty block.
				slot := uint64(0)
				for r := range BitsetRanges(dirty, blockSize) {
					for p := r.Start / pageSize; p < r.End()/pageSize; p++ {
						pg := mem[p*pageSize : (p+1)*pageSize]
						if header.IsZero(pg) {
							table[p] = pageRef{empty: true}
						} else {
							table[p] = pageRef{build: layerBuild, off: slot * uint64(pageSize)}
							slot++
							storedPages++
						}
					}
				}
			}

			hdr = buildHeader(layerBuild, table)
			copy(parent, mem)
		}

		return result{
			name:           name,
			storedMiB:      float64(storedPages) * float64(pageSize) / (1 << 20),
			parentFrames:   parentFrames,
			promotedFrames: promotedFrames,
			promotedPages:  promotedPages,
			hot:            resume(table, wsHot),
		}
	}

	results := []result{
		run("no-dedup", false, DedupBudget{}),
		run("dedup", true, DedupBudget{}),
		run("dedup+global", true, DedupBudget{MaxPromotedParentPagesTotal: 2048}),
		run("dedup+global+block", true, DedupBudget{
			MaxPromotedParentPagesTotal:    2048,
			MaxFetchWindowsPerBlock:        8,
			MaxPromotedParentPagesPerBlock: 64,
		}),
	}

	t.Logf("memfile %d MiB, %d cycles, working set %d blocks (%d MiB); fetch model: %.0fms TTFB, %.0f MB/s/stream, %vx concurrency",
		size/(1<<20), simCycles, simWorkingSetBlocks, int64(simWorkingSetBlocks)*blockSize/(1<<20), simTTFBMs, simBWMBps, simConcurrency)
	t.Logf("%-20s %12s %14s %14s %10s %10s %10s %10s", "scenario", "storedMiB", "lastParentFrm", "promoted(f/p)", "fetches", "fetchMiB", "estMs", "maxFanout")
	for _, r := range results {
		t.Logf("%-20s %12.1f %14d %7d/%-6d %10d %10.0f %10.0f %10d",
			r.name, r.storedMiB, r.parentFrames, r.promotedFrames, r.promotedPages, r.hot.fetches, r.hot.mib, r.hot.estMs, r.hot.maxFanout)
	}
}
