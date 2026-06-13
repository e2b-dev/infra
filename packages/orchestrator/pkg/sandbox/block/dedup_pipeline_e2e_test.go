//go:build linux

package block

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"testing"

	"github.com/RoaringBitmap/roaring/v2"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

// TestDedupPipelineE2E_NoCorruption replicates the full production
// pause→upload→resume data path for memfile-diff-dedup chains and asserts
// byte-exact restores after every generation:
//
//	pause:  real memfd → NewCacheFromMemfdDeduped (dedupCompare + inputEmpty
//	        merge + dedupDrain packing) → DiffMetadata.ToDiffHeader
//	        (CreateMapping/MergeMappings/NormalizeMappings → compact Mapping)
//	upload: zstd frame compression of the packed diff (storage.CompressBytes),
//	        CloneForUpload + ancestor BuildData propagation (full frame
//	        tables, V3 template sentinel), V4/V5 SerializeHeader — which
//	        sparse-trims ancestor frame tables to referenced ranges —
//	        then DeserializeBytes (a cross-node resume)
//	resume: every page resolved via GetShiftedMapping and read through the
//	        deserialized trimmed FrameTables (LocateCompressed → decompress
//	        frame → in-frame slice), exactly like chunker.locateChunk +
//	        OpenRangeReader
//
// The workload models hugepage-granular FC bitmaps: Dirty = 2 MiB blocks
// written this session (including identical rewrites), inputEmpty = balloon
// REMOVEd blocks (AndNot dirty, per uffd.DiffMetadata), zero writes, and
// reverts to ancestor content. Any misclassification, packing/offset bug,
// header merge/serialization bug, or frame-table trim/lookup bug fails the
// byte comparison.
func TestDedupPipelineE2E_NoCorruption(t *testing.T) {
	t.Parallel()

	budgets := map[string]DedupBudget{
		"none":   {},
		"global": {MaxPagesPerPromotedFrame: 256, BlockFaultPct: 40},
		"global+block": {
			MaxPagesPerPromotedFrame:       256,
			BlockFaultPct:                  40,
			MaxFetchWindowsPerBlock:        8,
			MaxPromotedParentPagesPerBlock: 64,
		},
	}

	for _, version := range []uint64{header.MetadataVersionV4, header.MetadataVersionV5} {
		for name, budget := range budgets {
			for seed := range int64(2) {
				for _, directIO := range []bool{false, true} {
					t.Run(fmt.Sprintf("v%d/%s/seed=%d/directIO=%v", version, name, seed, directIO), func(t *testing.T) {
						t.Parallel()
						runDedupPipelineChain(t, version, budget, seed, directIO)
					})
				}
			}
		}
	}
}

const (
	e2eBlockSize = int64(header.HugepageSize) // 2 MiB FC dirty-tracking granularity
	e2eBlocks    = 16                         // 32 MiB memfile
	e2eCycles    = 8
)

// e2eLayer is one uploaded build: the packed uncompressed diff payload, its
// zstd-compressed bytes, and the full frame table from the upload.
type e2eLayer struct {
	payload    []byte
	compressed []byte
	fullFT     *storage.FullFrameTable
}

func runDedupPipelineChain(t *testing.T, version uint64, budget DedupBudget, seed int64, directIO bool) {
	t.Helper()
	ctx := context.Background()

	// t.TempDir is often tmpfs, which rejects O_DIRECT; the drain falls back
	// nowhere (open fails loudly), so put directIO runs on a real filesystem.
	diffDir := func() string { return t.TempDir() }
	if directIO {
		base, err := os.MkdirTemp(".", "dedup-e2e-directio-")
		require.NoError(t, err)
		t.Cleanup(func() { _ = os.RemoveAll(base) })
		var n int
		diffDir = func() string {
			n++
			d := filepath.Join(base, fmt.Sprintf("c%d", n))
			require.NoError(t, os.Mkdir(d, 0o755))

			return d
		}
		probe, err := os.OpenFile(filepath.Join(base, "probe"), os.O_RDWR|os.O_CREATE|unix.O_DIRECT, 0o644)
		if err != nil {
			t.Skipf("filesystem does not support O_DIRECT: %v", err)
		}
		_ = probe.Close()
	}

	size := e2eBlockSize * e2eBlocks
	pagesPerBlock := e2eBlockSize / header.PageSize
	rng := rand.New(rand.NewSource(seed))

	// Template: uncompressed V3 build (2 MiB header block size, like prod
	// legacy templates), with a sprinkle of zero pages.
	template := make([]byte, size)
	_, err := rng.Read(template)
	require.NoError(t, err)
	for p := int64(0); p < size/header.PageSize; p++ {
		if rng.Float64() < 0.05 {
			clear(template[p*header.PageSize : (p+1)*header.PageSize])
		}
	}
	templateBuild := uuid.New()
	parentHdr, err := header.NewHeader(
		header.NewTemplateMetadata(templateBuild, uint64(e2eBlockSize), uint64(size)), nil)
	require.NoError(t, err)

	layers := map[uuid.UUID]*e2eLayer{
		templateBuild: {payload: template},
	}

	// mem is the live guest memory the snapshot must reproduce.
	mem := append([]byte(nil), template...)

	cfg := storage.CompressConfig{Enabled: true, Type: "zstd", Level: 2}

	for cycle := range e2eCycles {
		dirtyBlocks := roaring.New()
		inputEmpty := roaring.New()

		writePage := func(p int64, fill func([]byte)) {
			fill(mem[p*header.PageSize : (p+1)*header.PageSize])
			dirtyBlocks.Add(uint32(p / pagesPerBlock))
		}

		// Hot blocks: dense new writes.
		for range 2 {
			b := int64(rng.Intn(e2eBlocks))
			for p := b * pagesPerBlock; p < (b+1)*pagesPerBlock; p++ {
				if rng.Float64() < 0.6 {
					if rng.Float64() < 0.1 {
						writePage(p, func(pg []byte) { clear(pg) })
					} else {
						writePage(p, func(pg []byte) { _, _ = rng.Read(pg) })
					}
				}
			}
		}

		// Scattered writes: identical rewrites (dirty, unchanged), new
		// content, zeroes, and reverts to template content.
		for range 150 {
			p := int64(rng.Intn(int(size / header.PageSize)))
			switch r := rng.Float64(); {
			case r < 0.5:
				writePage(p, func([]byte) {}) // identical rewrite
			case r < 0.7:
				writePage(p, func(pg []byte) { _, _ = rng.Read(pg) })
			case r < 0.8:
				writePage(p, func(pg []byte) { clear(pg) })
			default:
				writePage(p, func(pg []byte) {
					copy(pg, template[p*header.PageSize:(p+1)*header.PageSize])
				})
			}
		}

		// Balloon REMOVE: one block zeroed. If untouched this session it
		// lands in inputEmpty (uffd tracker Zero state, AndNot dirty);
		// if already dirty it stays dirty and scans as zeros.
		rb := uint32(rng.Intn(e2eBlocks))
		clear(mem[int64(rb)*e2eBlockSize : (int64(rb)+1)*e2eBlockSize])
		if !dirtyBlocks.Contains(rb) {
			inputEmpty.Add(rb)
		}

		// ---- Pause: production memfd dedup path ----
		memfd := newE2EMemfd(t, mem)
		base := &e2eChainDevice{t: t, hdr: parentHdr, layers: layers}
		outPath := filepath.Join(diffDir(), fmt.Sprintf("diff-%d", cycle))
		metaOut := utils.NewSetOnce[*header.DiffMetadata]()

		d, err := NewCacheFromMemfdDeduped(ctx, base, e2eBlockSize, outPath, memfd,
			dirtyBlocks, false, directIO, budget, inputEmpty, metaOut)
		require.NoError(t, err)

		meta, err := metaOut.Wait()
		require.NoError(t, err, "cycle %d: dedup compare", cycle)
		cache, err := d.Wait(ctx)
		require.NoError(t, err, "cycle %d: dedup drain", cycle)

		payloadSize := int64(meta.Dirty.GetCardinality()) * header.PageSize
		payload := make([]byte, payloadSize)
		if payloadSize > 0 {
			_, err = cache.ReadAt(payload, 0)
			require.NoError(t, err)
		}
		require.NoError(t, d.Close())

		newBuild := uuid.New()
		diffHdr, err := meta.ToDiffHeader(ctx, parentHdr, newBuild)
		require.NoError(t, err, "cycle %d: to diff header", cycle)

		// ---- Upload: compress + finalize + serialize round-trip ----
		layer := &e2eLayer{payload: payload}
		var selfBuild header.BuildData
		if payloadSize > 0 {
			fullFT, compressed, _, err := storage.CompressBytes(ctx, payload, cfg)
			require.NoError(t, err, "cycle %d: compress", cycle)
			layer.compressed = compressed
			layer.fullFT = fullFT
			selfBuild = header.BuildData{Size: payloadSize, FrameData: fullFT.Table()}
		}
		layers[newBuild] = layer

		finalized := diffHdr.CloneForUpload(version)
		finalized.IncompletePendingUpload = false
		if finalized.Builds == nil {
			finalized.Builds = make(map[uuid.UUID]header.BuildData)
		}
		// appendAncestorBuilds: overwrite with the ancestor's authoritative
		// self entry (full FT); V3 ancestors get the sentinel empty entry.
		for _, id := range diffHdr.Mapping.Builds() {
			if id == newBuild || id == uuid.Nil {
				continue
			}
			anc := layers[id]
			require.NotNil(t, anc, "cycle %d: mapping references unknown build %s", cycle, id)
			if anc.fullFT != nil {
				finalized.Builds[id] = header.BuildData{
					Size:      int64(len(anc.payload)),
					FrameData: anc.fullFT.Table(),
				}
			} else {
				finalized.Builds[id] = header.BuildData{}
			}
		}
		finalized.Builds[newBuild] = selfBuild

		serialized, err := header.SerializeHeader(finalized)
		require.NoError(t, err, "cycle %d: serialize header", cycle)
		parentHdr, err = header.DeserializeBytes(serialized)
		require.NoError(t, err, "cycle %d: deserialize header", cycle)

		// ---- Resume: verify every page byte-exactly ----
		resumed := &e2eChainDevice{t: t, hdr: parentHdr, layers: layers}
		for off := int64(0); off < size; off += header.PageSize {
			got, err := resumed.readPage(ctx, off)
			require.NoError(t, err, "cycle %d: read page %d", cycle, off/header.PageSize)
			want := mem[off : off+header.PageSize]
			if !bytes.Equal(want, got) {
				t.Fatalf("cycle %d: page %d restored bytes differ (block %d, dirty=%v, inputEmpty=%v)",
					cycle, off/header.PageSize, off/e2eBlockSize,
					dirtyBlocks.Contains(uint32(off/e2eBlockSize)), inputEmpty.Contains(uint32(off/e2eBlockSize)))
			}
		}
	}
}

func newE2EMemfd(t *testing.T, data []byte) *Memfd {
	t.Helper()

	fd, err := unix.MemfdCreate("e2e", 0)
	require.NoError(t, err)
	require.NoError(t, unix.Ftruncate(fd, int64(len(data))))
	_, err = unix.Pwrite(fd, data, 0)
	require.NoError(t, err)

	m, err := NewFromFd(fd)
	require.NoError(t, err)

	return m
}

// e2eChainDevice implements ReadonlyDevice over a header + uploaded layers,
// reading compressed layers through their (possibly trimmed) FrameTables the
// way the chunker does: locate the frame containing the U-offset, decompress
// it whole, slice the in-frame range.
type e2eChainDevice struct {
	t      *testing.T
	hdr    *header.Header
	layers map[uuid.UUID]*e2eLayer

	frameCache map[e2eFrameKey][]byte
}

type e2eFrameKey struct {
	build  uuid.UUID
	startU int64
}

func (d *e2eChainDevice) Header() *header.Header      { return d.hdr }
func (d *e2eChainDevice) SwapHeader(h *header.Header) { d.hdr = h }
func (d *e2eChainDevice) BlockSize() int64            { return e2eBlockSize }
func (d *e2eChainDevice) Close() error                { return nil }
func (d *e2eChainDevice) Size(context.Context) (int64, error) {
	return int64(d.hdr.Metadata.Size), nil
}

func (d *e2eChainDevice) readPage(ctx context.Context, off int64) ([]byte, error) {
	m, err := d.hdr.GetShiftedMapping(ctx, off)
	if err != nil {
		return nil, err
	}
	if int64(m.Length) < header.PageSize {
		return nil, fmt.Errorf("mapping at %d shorter than a page: %d", off, m.Length)
	}
	if m.BuildId == uuid.Nil {
		return make([]byte, header.PageSize), nil
	}

	return d.readBuildRange(m.BuildId, int64(m.Offset), header.PageSize)
}

// readBuildRange reads [uOff, uOff+n) of a build's uncompressed space using
// the frame table recorded in the current header for that build.
func (d *e2eChainDevice) readBuildRange(buildID uuid.UUID, uOff, n int64) ([]byte, error) {
	layer := d.layers[buildID]
	if layer == nil {
		return nil, fmt.Errorf("unknown build %s", buildID)
	}

	ft := d.hdr.GetBuildFrameData(buildID)
	if !ft.IsCompressed() {
		if uOff+n > int64(len(layer.payload)) {
			return nil, fmt.Errorf("uncompressed read [%d,%d) past layer end %d (build %s)",
				uOff, uOff+n, len(layer.payload), buildID)
		}

		return layer.payload[uOff : uOff+n], nil
	}

	out := make([]byte, 0, n)
	for cur := uOff; cur < uOff+n; {
		u, err := ft.LocateUncompressed(cur)
		if err != nil {
			return nil, fmt.Errorf("locate U %d (build %s): %w", cur, buildID, err)
		}
		frame, err := d.frameBytes(buildID, layer, ft, u)
		if err != nil {
			return nil, err
		}
		end := min(uOff+n, u.Offset+int64(u.Length))
		out = append(out, frame[cur-u.Offset:end-u.Offset]...)
		cur = end
	}

	return out, nil
}

func (d *e2eChainDevice) frameBytes(buildID uuid.UUID, layer *e2eLayer, ft *storage.FrameTable, u storage.Range) ([]byte, error) {
	key := e2eFrameKey{buildID, u.Offset}
	if b, ok := d.frameCache[key]; ok {
		return b, nil
	}

	c, err := ft.LocateCompressed(u.Offset)
	if err != nil {
		return nil, fmt.Errorf("locate C %d (build %s): %w", u.Offset, buildID, err)
	}
	if c.Offset+int64(c.Length) > int64(len(layer.compressed)) {
		return nil, fmt.Errorf("C range [%d,%d) past compressed end %d (build %s)",
			c.Offset, c.Offset+int64(c.Length), len(layer.compressed), buildID)
	}

	dec, err := storage.NewDecompressingReader(
		bytes.NewReader(layer.compressed[c.Offset:c.Offset+int64(c.Length)]), ft.CompressionType())
	if err != nil {
		return nil, err
	}
	defer dec.Close()

	frame, err := io.ReadAll(dec)
	if err != nil {
		return nil, fmt.Errorf("decompress frame at C %d (build %s): %w", c.Offset, buildID, err)
	}
	if len(frame) != u.Length {
		return nil, fmt.Errorf("frame at U %d decompressed to %d bytes, frame table says %d (build %s)",
			u.Offset, len(frame), u.Length, buildID)
	}

	if d.frameCache == nil {
		d.frameCache = make(map[e2eFrameKey][]byte)
	}
	d.frameCache[key] = frame

	return frame, nil
}

func (d *e2eChainDevice) Slice(ctx context.Context, off, length int64) ([]byte, error) {
	out := make([]byte, 0, length)
	for cur := off; cur < off+length; {
		m, err := d.hdr.GetShiftedMapping(ctx, cur)
		if err != nil {
			return nil, err
		}
		n := min(int64(m.Length), off+length-cur)
		if n <= 0 {
			return nil, fmt.Errorf("zero-length mapping at %d", cur)
		}
		if m.BuildId == uuid.Nil {
			out = append(out, make([]byte, n)...)
		} else {
			b, err := d.readBuildRange(m.BuildId, int64(m.Offset), n)
			if err != nil {
				return nil, err
			}
			out = append(out, b...)
		}
		cur += n
	}

	return out, nil
}

func (d *e2eChainDevice) ReadAt(ctx context.Context, p []byte, off int64) (int, error) {
	b, err := d.Slice(ctx, off, int64(len(p)))
	if err != nil {
		return 0, err
	}

	return copy(p, b), nil
}

var _ ReadonlyDevice = (*e2eChainDevice)(nil)
