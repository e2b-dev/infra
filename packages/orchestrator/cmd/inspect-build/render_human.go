package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"slices"
	"sort"
	"strings"

	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

const (
	heatmapWidth = 100  // cells per wrapped row
	heatmapCells = 1000 // max cells across a whole heatmap
)

// view controls hex/decimal formatting, which sections expand to full detail,
// the offset:size range applied to expanded lists, and the terminal width
// available for full-width heatmap rows.
type view struct {
	decimal bool
	expand  map[string]bool // section name → expanded; "all" expands every section
	rng     span            // --range: limits expanded mapping/frame lists
	width   int             // terminal columns; falls back to a default when piped
}

// --expand section identifiers.
const (
	sectionMappings = "mappings"
	sectionFrames   = "frames"
	sectionMetadata = "metadata"
	sectionAll      = "all"
)

func (vw view) expanded(section string) bool {
	return vw.expand[sectionAll] || vw.expand[section]
}

// span is an inclusive-start, exclusive-end filter range.
type span struct {
	set        bool
	start, end uint64
}

func (s span) overlaps(start, end uint64) bool {
	return !s.set || (start < s.end && end > s.start)
}

const (
	ansiReset = "\x1b[0m"
	ansiBold  = "\x1b[1m"
	ansiDim   = "\x1b[2m"
)

// rgb is a true-color value that can paint a foreground or a background.
type rgb struct{ r, g, b uint8 }

func (c rgb) fg() string { return fmt.Sprintf("\x1b[38;2;%d;%d;%dm", c.r, c.g, c.b) }

// num renders an exact offset/size — hex by default, decimal with --decimal.
func (vw view) num(v uint64) string {
	if vw.decimal {
		return fmt.Sprintf("%d", v)
	}

	return fmt.Sprintf("0x%X", v)
}

// size pairs an exact value with its human-readable magnitude, or "unknown"
// for a sizeUnknown sentinel.
func (vw view) size(v int64) string {
	if v < 0 {
		return "unknown"
	}

	return fmt.Sprintf("%s (%s)", vw.num(uint64(v)), humanSize(v))
}

func humanSize(b int64) string {
	const u = 1024
	switch {
	case b < 0:
		return "unknown"
	case b >= u*u*u:
		return fmt.Sprintf("%.1f GiB", float64(b)/(u*u*u))
	case b >= u*u:
		return fmt.Sprintf("%.1f MiB", float64(b)/(u*u))
	case b >= u:
		return fmt.Sprintf("%.1f KiB", float64(b)/u)
	default:
		return fmt.Sprintf("%d B", b)
	}
}

// zeroColor is the dim swatch for nil (zero-block) mappings.
func zeroColor() rgb { return rgb{70, 70, 70} }

// buildPalette assigns each build ID a stable color so the mappings heatmap,
// the mappings summary, and the builds list all cross-reference by swatch.
// The current build is always green; zero blocks are dim.
func buildPalette(byBuild []buildExtent) map[uuid.UUID]rgb {
	currentColor := rgb{120, 220, 130} // green — the current build
	swatches := []rgb{
		{80, 200, 220},  // cyan
		{100, 150, 235}, // blue
		{200, 130, 225}, // magenta
		{230, 165, 70},  // amber
		{225, 110, 110}, // red
	}
	colors := map[uuid.UUID]rgb{uuid.Nil: zeroColor()}
	next := 0
	for _, b := range byBuild {
		if _, seen := colors[b.BuildID]; seen {
			continue
		}
		if b.Role == roleCurrent {
			colors[b.BuildID] = currentColor

			continue
		}
		colors[b.BuildID] = swatches[next%len(swatches)]
		next++
	}

	return colors
}

func colorOf(colors map[uuid.UUID]rgb, id uuid.UUID) rgb {
	if c, ok := colors[id]; ok {
		return c
	}

	return zeroColor()
}

func swatch(c rgb) string { return c.fg() + "█" + ansiReset }

// heatColor maps t in [0,1] to a red→yellow→green gradient.
func heatColor(t float64) rgb {
	t = max(0, min(1, t))
	if t < 0.5 {
		return rgb{220, 60 + uint8(t*2*140), 60}
	}

	return rgb{220 - uint8((t-0.5)*2*160), 200, 60 + uint8((t-0.5)*2*30)}
}

// blue paints the compression type.
func blue(s string) string { return rgb{90, 160, 245}.fg() + s + ansiReset }

// ratioColored paints a compression ratio on the same red→green heat gradient
// used by the frame heatmap.
func ratioColored(r float64) string {
	return heatColor(ratioNorm(r)).fg() + fmt.Sprintf("%.2fx", r) + ansiReset
}

func renderHuman(w io.Writer, chain []*report, vw view) {
	head := chain[0]
	colors := buildPalette(head.Mappings.ByBuild)
	renderHeader(w, head, vw)
	renderImage(w, head, vw)
	renderMappings(w, head, vw, colors)
	renderBuilds(w, chain, vw, colors)
	renderData(w, chain, vw)
	if len(chain) == 1 {
		renderFrameMap(w, head, vw, colors)
	}
	renderMetadata(w, head, vw)
}

func section(w io.Writer, title string) {
	fmt.Fprintf(w, "\n%s%s%s%s\n", ansiBold, rgb{120, 220, 180}.fg(), title, ansiReset)
}

func field(w io.Writer, label, value string) {
	fmt.Fprintf(w, "  %s%-16s%s %s\n", ansiDim, label, ansiReset, value)
}

// fieldIf prints a field only when the value is non-empty.
func fieldIf(w io.Writer, label, value string) {
	if value != "" {
		field(w, label, value)
	}
}

// listHeader labels an expanded list, noting the active --range.
func listHeader(w io.Writer, vw view, kind string) {
	label := kind
	if vw.rng.set {
		label += fmt.Sprintf(" · range %s + %s", vw.num(vw.rng.start), vw.num(vw.rng.end-vw.rng.start))
	}
	fmt.Fprintf(w, "\n  %s── %s ──%s\n", ansiDim, label, ansiReset)
}

func renderHeader(w io.Writer, r *report, vw view) {
	section(w, "HEADER")

	field(w, "Artifact", fmt.Sprintf("%s%s%s", ansiBold, r.Artifact, ansiReset))
	field(w, "Source", r.Source)

	verColor := rgb{230, 150, 60}.fg() // orange — V3 / legacy
	if r.Header.Version >= 4 {
		verColor = rgb{120, 220, 180}.fg() // green — V4+
	}
	field(w, "Version", fmt.Sprintf("%sV%d%s", verColor, r.Header.Version, ansiReset))

	if r.Header.HeaderSize > 0 {
		hdr := vw.size(r.Header.HeaderSize)
		if r.Header.HeaderRatio > 0 {
			hdr += "  " + ratioColored(r.Header.HeaderRatio)
		}
		field(w, "Header size", hdr)
	}

	storage := vw.size(r.Header.StorageSize)
	if r.Header.Ratio > 0 {
		storage += "  " + ratioColored(r.Header.Ratio)
	}
	field(w, "Storage size", storage)
}

func renderImage(w io.Writer, r *report, vw view) {
	section(w, "IMAGE")
	field(w, "Virtual size", vw.size(int64(r.Image.VirtualSize)))
	field(w, "Diff size", vw.size(r.Image.DiffSize))
	field(w, "Build ID", r.Image.BuildID.String())
	if r.Image.BaseBuildID != r.Image.BuildID {
		field(w, "Base build ID", r.Image.BaseBuildID.String())
	}
	field(w, "Ancestors", fmt.Sprintf("%d", r.Image.Ancestors))
	fieldIf(w, "From image", r.Image.FromImage)
	fieldIf(w, "Kernel", r.Image.Kernel)
	fieldIf(w, "Firecracker", r.Image.Firecracker)
	fieldIf(w, "User", r.Image.User)
}

// renderMetadata prints metadata.json as a structured key/value tree, kept out
// of IMAGE so its bulk doesn't disrupt the dashboard.
func renderMetadata(w io.Writer, r *report, vw view) {
	if !vw.expanded(sectionMetadata) || len(r.Image.Metadata) == 0 {
		return
	}

	dec := json.NewDecoder(bytes.NewReader(r.Image.Metadata))
	dec.UseNumber() // keep integers out of float scientific notation
	var v any
	if dec.Decode(&v) != nil {
		return
	}
	m, ok := v.(map[string]any)
	if !ok {
		return
	}

	section(w, "METADATA")
	renderMetaMap(w, m, 1)
}

// renderMetaMap prints a parsed JSON object as an indented key/value tree;
// arrays are summarized by length so bulky prefetch lists don't flood the view.
func renderMetaMap(w io.Writer, m map[string]any, depth int) {
	indent := strings.Repeat("  ", depth)
	// Top-level keys and object keys are bold; nested scalars are dim.
	keyStyle := ansiDim
	if depth == 1 {
		keyStyle = ansiBold
	}
	for _, k := range slices.Sorted(maps.Keys(m)) {
		switch val := m[k].(type) {
		case map[string]any:
			fmt.Fprintf(w, "%s%s%s%s\n", indent, ansiBold, k, ansiReset)
			renderMetaMap(w, val, depth+1)
		case []any:
			fmt.Fprintf(w, "%s%s%-22s%s %d items\n", indent, keyStyle, k, ansiReset, len(val))
		default:
			fmt.Fprintf(w, "%s%s%-22s%s %v\n", indent, keyStyle, k, ansiReset, val)
		}
	}
}

func renderMappings(w io.Writer, r *report, vw view, colors map[uuid.UUID]rgb) {
	section(w, fmt.Sprintf("MAPPINGS (%d)", r.Mappings.Count))

	for _, e := range r.Mappings.ByBuild {
		fmt.Fprintf(w, "  %s %s  %-8s %s  %d mappings\n",
			swatch(colorOf(colors, e.BuildID)), e.BuildID, e.Role, vw.size(int64(e.Bytes)), e.Mappings)
	}

	if !vw.expanded(sectionMappings) {
		return
	}
	listHeader(w, vw, "mappings")
	for _, m := range filteredMappings(r.Mappings.List, vw.rng) {
		fmt.Fprintf(w, "  %s %-12s + %-9s  %s\n",
			swatch(colorOf(colors, m.BuildID)), vw.num(m.Offset), vw.num(m.Length), m.BuildID)
	}
}

// renderBuilds lists the build layers. With --recursive each build is a card
// enriched by gathering it; otherwise it is the compact one-per-line list.
func renderBuilds(w io.Writer, chain []*report, vw view, colors map[uuid.UUID]rgb) {
	head := chain[0]

	if len(chain) == 1 {
		section(w, fmt.Sprintf("BUILDS (%d)", len(head.Builds)))
		for _, b := range head.Builds {
			renderBuildLine(w, b, vw, colors)
		}

		return
	}

	section(w, fmt.Sprintf("BUILDS (%d, dependency order)", len(chain)))
	for _, c := range chain {
		renderBuildCard(w, head, c, colors)
	}
}

// renderBuildLine prints one build's short card: identity, role, compression,
// ratio, sizes, frame count, checksum.
func renderBuildLine(w io.Writer, b buildInfo, vw view, colors map[uuid.UUID]rgb) {
	line := fmt.Sprintf("%s %s  %-8s ", swatch(colorOf(colors, b.BuildID)), b.BuildID, b.Role)
	if b.Ratio > 0 {
		line += blue(b.Compression) + "  " + ratioColored(b.Ratio)
	} else {
		line += b.Compression
	}
	line += "  " + humanSize(b.UncompressedSize)
	if b.FrameCount > 0 {
		line += fmt.Sprintf("  %d frames", b.FrameCount)
	}
	fmt.Fprintf(w, "  %s\n", line)

	fmt.Fprintf(w, "      uncompressed %s\n", vw.size(b.UncompressedSize))
	if b.CompressedSize > 0 {
		fmt.Fprintf(w, "      compressed   %s in %d frames\n", vw.size(b.CompressedSize), b.FrameCount)
	}
	if b.Checksum != "" {
		fmt.Fprintf(w, "      %s\n", b.Checksum)
	}
}

// renderBuildCard prints one build in --recursive mode: identity and
// compression, what the target header records about it ("header"), what
// gathering it revealed ("build"), and how much of it the target uses ("usage").
func renderBuildCard(w io.Writer, head, c *report, colors map[uuid.UUID]rgb) {
	id := c.Image.BuildID

	verColor := rgb{230, 150, 60} // orange — V3
	if c.Header.Version >= 4 {
		verColor = rgb{120, 220, 180} // green — V4+
	}
	line := fmt.Sprintf("%s %s  %-8s %sV%d%s", swatch(colorOf(colors, id)),
		id, roleOf(id, head.h.Metadata), verColor.fg(), c.Header.Version, ansiReset)
	if c.Data.Compressed {
		line += "  " + blue(c.Data.CompressionType) + " " + ratioColored(c.Data.Ratio)
	} else {
		line += "  uncompressed"
	}
	fmt.Fprintf(w, "\n  %s\n", line)

	if b, ok := head.buildInfoFor(id); ok {
		// The header's contribution: the compressed bytes the read path
		// downloads for this build, plus its integrity checksum.
		rec := humanSize(b.UncompressedSize) + " uncompressed"
		if b.CompressedSize > 0 {
			rec = humanSize(b.CompressedSize) + " compressed"
		}
		if b.Checksum != "" {
			rec += " · " + b.Checksum
		}
		cardField(w, "header", rec)
	}

	origin := fmt.Sprintf("%d ancestors", c.Image.Ancestors)
	if c.Image.Ancestors == 1 {
		origin = "1 ancestor"
	}
	if c.Image.FromImage != "" {
		origin = "from " + c.Image.FromImage + " · " + origin
	}
	build := fmt.Sprintf("virtual %s · diff %s", humanSize(int64(c.Image.VirtualSize)), humanSize(c.Image.DiffSize))
	if c.Data.Compressed {
		build += fmt.Sprintf(" · %d frames", c.Data.FrameCount)
	}
	cardField(w, "build", build+" · "+origin)

	if u := c.Usage; u != nil {
		cardField(w, "usage", usageSummary(u))
	}
}

// usageSummary describes how much of an ancestor the target build draws on.
func usageSummary(u *ancestorUsage) string {
	if u.UsedBytes == 0 {
		return "unused"
	}
	s := humanSize(u.UsedBytes)
	if u.DiffBytes > 0 {
		s += fmt.Sprintf(" · %.1f%% of diff", u.UsedFraction*100)
	}
	if u.TotalFrames > 0 {
		s += fmt.Sprintf(" · %d/%d frames touched", u.FramesTouched, u.TotalFrames)
	}

	return s
}

// cardField prints a dim, labeled, indented build-card sub-line.
func cardField(w io.Writer, label, value string) {
	fmt.Fprintf(w, "      %s%-8s%s %s\n", ansiDim, label, ansiReset, value)
}

func renderData(w io.Writer, chain []*report, vw view) {
	head := chain[0]
	section(w, "DATA (current build)")
	d := head.Data
	if d.Compressed {
		field(w, "Compression", blue(d.CompressionType))
		field(w, "Ratio", ratioColored(d.Ratio))
		field(w, "Uncompressed", vw.size(d.UncompressedSize))
		field(w, "Compressed", vw.size(d.CompressedSize))
		field(w, "Frames", fmt.Sprintf("%d", d.FrameCount))
	} else {
		field(w, "Compression", "none")
		field(w, "Size", vw.size(d.UncompressedSize))
	}

	// With --recursive, the per-build chain heatmap shows every frame across
	// the chain; the single-build view gets the unified FRAMEMAP further down.
	if len(chain) > 1 {
		renderChainFrames(w, chain)
	}

	if !d.Compressed || !vw.expanded(sectionFrames) {
		return
	}
	listHeader(w, vw, "frames")
	for _, f := range framesInRange(d.Frames, head.h, head.h.Metadata.BuildId, vw.rng) {
		fr := frameRatio(f)
		fmt.Fprintf(w, "  %s U %s + %s  C %s + %s  %.2fx\n",
			swatch(heatColor(ratioNorm(fr))),
			vw.num(uint64(f.StartU)), vw.num(uint64(f.EndU-f.StartU)),
			vw.num(uint64(f.StartC)), vw.num(uint64(f.EndC-f.StartC)),
			fr)
	}
}

// renderChainFrames draws one heatmap of every build's data across the chain:
// compression-colored per frame for compressed builds, blue per chunk for
// uncompressed ones (raw data has no ratio, so it sits off the heat gradient).
func renderChainFrames(w io.Writer, chain []*report) {
	var cells []string
	builds, anyUncompressed := 0, false
	for _, c := range chain {
		bc := buildHeatCells(c)
		if len(bc) == 0 {
			continue
		}
		cells = append(cells, bc...)
		builds++
		anyUncompressed = anyUncompressed || !c.Data.Compressed
	}
	if len(cells) == 0 {
		return
	}

	label := fmt.Sprintf("%d frames across %d builds", len(cells), builds)
	if anyUncompressed {
		label += " · blue = uncompressed"
	}
	fmt.Fprintf(w, "\n  %s── %s ──%s\n", ansiDim, label, ansiReset)
	heatmap(w, cells)
	ratioLegend(w)
}

// buildHeatCells renders one build's data as heatmap cells: compression-colored
// per frame when compressed, or blue per MemoryChunkSize chunk when not.
func buildHeatCells(c *report) []string {
	if c.Data.Compressed {
		return frameCells(c.Data.Frames)
	}
	if c.Data.UncompressedSize <= 0 {
		return nil
	}

	chunk := int64(storage.MemoryChunkSize)
	n := min(int((c.Data.UncompressedSize+chunk-1)/chunk), heatmapCells)
	cell := blue("█")
	cells := make([]string, n)
	for i := range cells {
		cells[i] = cell
	}

	return cells
}

// ratioLegend prints the red→green scale used for ratios and the frame heatmap.
func ratioLegend(w io.Writer) {
	var b strings.Builder
	b.WriteString("  " + ansiDim + "ratio 1x " + ansiReset)
	for i := range 21 {
		b.WriteString(heatColor(float64(i) / 20).fg())
		b.WriteString("█")
		b.WriteString(ansiReset)
	}
	b.WriteString(ansiDim + " 10x+" + ansiReset + "\n")
	fmt.Fprintln(w, b.String())
}

// renderFrameMap draws four stacked, column-aligned rows over the virtual
// address space: one cell per hugepage-sized chunk.
//
//	C: compression ratio of the SELF frame(s) covering each chunk
//	   (ancestor-only chunks show as sparse — we don't have ancestor frame
//	   tables in scope)
//	F: cold-restore fetch fanout: distinct backing fetches across all builds
//	B: build provenance — the build that serves the most bytes in each
//	   chunk, colored to match the BUILDS palette above
//	M: mapping density: number of distinct mappings touching each chunk
//	   (high count = fragmented; tends to correlate with F)
//
// C/F/M use the same red→green heatColor gradient so doubly-bad zones jump
// out as vertical hot stripes; B uses the buildPalette swatches. Legends
// for C/F/M ride the section header so the data rows can spread the full
// terminal width.
func renderFrameMap(w io.Writer, r *report, vw view, colors map[uuid.UUID]rgb) {
	if r.Fetchmap == nil || r.Fetchmap.ChunkCount == 0 {
		return
	}
	fm := r.Fetchmap
	bs := fm.ChunkSize

	cmp := compressionPerChunk(r.h, r.Data.Frames, fm.ChunkCount, bs)
	dens := mappingDensityPerChunk(r.h, fm.ChunkCount, bs)
	builds := buildPerChunk(r.h, fm.ChunkCount, bs)
	width := min(fm.ChunkCount, heatmapRowWidth(vw))
	cmpRow := framemapRow(cmp, fm.ChunkCount, width, compressionCell, worstCompression)
	fetchRow := framemapRow(intsToFloat(fm.Cells), fm.ChunkCount, width, densityCell, worstFetch)
	buildRow := buildmapRow(builds, fm.ChunkCount, width, colors)
	densRow := framemapRow(dens, fm.ChunkCount, width, densityCell, worstFetch)

	avgDensity := averageNonZero(dens)
	title := fmt.Sprintf("HEATMAP %s · %d × %s (%s)",
		r.Artifact, fm.ChunkCount, vw.num(uint64(bs)), humanSize(int64(fm.ChunkCount)*bs))
	cmpAvg := ""
	if r.Data.Ratio > 0 {
		cmpAvg = " avg " + ratioColored(r.Data.Ratio)
	}
	cmpLegend := "C: " + gradientLegend("10x+", "1x", 1.0, 0.0) + cmpAvg
	fetchLegend := "F: " + gradientLegend("2", "8+", 1.0, 0.0) +
		fmt.Sprintf(" avg %.2f", fm.AvgSegments)
	densLegend := "M: " + gradientLegend("2", "8+", 1.0, 0.0) +
		fmt.Sprintf(" avg %.2f", avgDensity)

	// Section header: title + C/F/M legends on one line; data rows below
	// get the C: / F: / B: / M: prefixes. B has no scalar legend — its
	// colors match the BUILDS palette above.
	fmt.Fprintf(w, "\n%s%s%s%s   %s   %s   %s\n",
		ansiBold, rgb{120, 220, 180}.fg(), title, ansiReset, cmpLegend, fetchLegend, densLegend)
	fmt.Fprintln(w)
	fmt.Fprintf(w, "  B: %s\n", buildRow)
	fmt.Fprintf(w, "  C: %s\n", cmpRow)
	fmt.Fprintf(w, "  F: %s\n", fetchRow)
	fmt.Fprintf(w, "  M: %s\n", densRow)
}

// buildPerChunk maps each virtual chunk to the build ID whose mappings
// cover the most bytes inside that chunk. Nil mappings are excluded — they
// carry no fetch cost so they shouldn't outvote a real backing build. A
// chunk with no non-Nil mappings (fully sparse) stays at uuid.Nil.
func buildPerChunk(h *header.Header, chunkCount int, chunkSize int64) []uuid.UUID {
	winners := make([]uuid.UUID, chunkCount)
	bytesPer := make([]map[uuid.UUID]uint64, chunkCount)
	for _, m := range h.Mapping.All() {
		if m.Length == 0 || m.BuildId == uuid.Nil {
			continue
		}
		vStart := int64(m.Offset)
		vEnd := vStart + int64(m.Length)
		first := vStart / chunkSize
		last := (vEnd - 1) / chunkSize
		if last >= int64(chunkCount) {
			last = int64(chunkCount - 1)
		}
		for c := first; c <= last; c++ {
			cStart := c * chunkSize
			cEnd := cStart + chunkSize
			overlap := uint64(min(vEnd, cEnd) - max(vStart, cStart))
			if bytesPer[c] == nil {
				bytesPer[c] = map[uuid.UUID]uint64{}
			}
			bytesPer[c][m.BuildId] += overlap
		}
	}
	for c := range winners {
		var winner uuid.UUID
		var top uint64
		for id, n := range bytesPer[c] {
			if n > top {
				top, winner = n, id
			}
		}
		winners[c] = winner
	}

	return winners
}

// mappingDensityPerChunk counts how many distinct backing mappings overlap
// each chunk — sparse (Nil) mappings are excluded since they carry no fetch
// cost. With Nil out, M matches F's "fetchable layers" semantics, and the
// invariant M ≤ (number of distinct builds touching the chunk) holds.
func mappingDensityPerChunk(h *header.Header, chunkCount int, chunkSize int64) []float64 {
	out := make([]float64, chunkCount)
	for _, m := range h.Mapping.All() {
		if m.Length == 0 || m.BuildId == uuid.Nil {
			continue
		}
		first := int64(m.Offset) / chunkSize
		last := (int64(m.Offset) + int64(m.Length) - 1) / chunkSize
		if last >= int64(chunkCount) {
			last = int64(chunkCount - 1)
		}
		for c := first; c <= last; c++ {
			out[c]++
		}
	}

	return out
}

// buildmapRow downsamples a per-chunk winner-build series to width cells.
// Each output cell picks the most common non-Nil winner across its bucket
// (ties broken by first occurrence). A cell is only rendered as Nil when
// every input chunk in its bucket is Nil — otherwise we'd hide real
// backing builds whenever a bucket is mostly-but-not-all sparse.
func buildmapRow(winners []uuid.UUID, n, width int, colors map[uuid.UUID]rgb) string {
	if n == 0 || width == 0 {
		return ""
	}
	perCell := (n + width - 1) / width
	var b strings.Builder
	counts := map[uuid.UUID]int{}
	for i := 0; i < n; i += perCell {
		hi := min(i+perCell, n)
		clear(counts)
		var top uuid.UUID
		var topN int
		for _, id := range winners[i:hi] {
			if id == uuid.Nil {
				continue
			}
			counts[id]++
			if counts[id] > topN {
				topN, top = counts[id], id
			}
		}
		if top == uuid.Nil {
			b.WriteString(ansiDim)
			b.WriteString("·")
			b.WriteString(ansiReset)

			continue
		}
		b.WriteString(colorOf(colors, top).fg())
		b.WriteString("█")
		b.WriteString(ansiReset)
	}

	return b.String()
}

// averageNonZero returns the mean of the non-zero entries in v, or 0 if
// there are none. Used for the M legend's "avg N" stat.
func averageNonZero(v []float64) float64 {
	var sum float64
	var n int
	for _, x := range v {
		if x > 0 {
			sum += x
			n++
		}
	}
	if n == 0 {
		return 0
	}

	return sum / float64(n)
}

// heatmapRowWidth is the cell budget for a single data row: terminal width
// minus the "  C: " / "  F: " prefix (5 chars). Falls back to heatmapWidth
// when the terminal is too narrow or unset (piped output).
func heatmapRowWidth(vw view) int {
	const prefix = 5
	avail := vw.width - prefix
	if avail < 20 {
		return heatmapWidth
	}

	return avail
}

// gradientLegend renders "leftLabel <gradient strip> rightLabel" where the
// strip walks heatColor from leftHeat to rightHeat across a fixed number of
// cells, and each label is tinted by its endpoint heat. Use leftHeat=0 (red)
// + rightHeat=1 (green) for "low = bad, high = good" semantics; flip them
// when low values are the desirable end.
func gradientLegend(leftLabel, rightLabel string, leftHeat, rightHeat float64) string {
	const cells = 16
	var b strings.Builder
	b.WriteString(heatColor(leftHeat).fg())
	b.WriteString(leftLabel)
	b.WriteString(ansiReset)
	b.WriteByte(' ')
	for i := range cells {
		t := leftHeat + (rightHeat-leftHeat)*float64(i)/float64(cells-1)
		b.WriteString(heatColor(t).fg())
		b.WriteString("█")
		b.WriteString(ansiReset)
	}
	b.WriteByte(' ')
	b.WriteString(heatColor(rightHeat).fg())
	b.WriteString(rightLabel)
	b.WriteString(ansiReset)

	return b.String()
}

// framemapRow downsamples a per-block series to width cells by picking the
// worst value in each bucket, then renders each cell via cellFn.
func framemapRow(src []float64, n, width int, cellFn func(float64) string, worst func(a, b float64) float64) string {
	if n == 0 || width == 0 {
		return ""
	}
	perCell := (n + width - 1) / width
	var b strings.Builder
	for i := 0; i < n; i += perCell {
		hi := min(i+perCell, n)
		w := src[i]
		for _, v := range src[i+1 : hi] {
			w = worst(w, v)
		}
		b.WriteString(cellFn(w))
	}

	return b.String()
}

// compressionPerChunk maps each virtual block to the compression ratio of
// the SELF frame(s) covering it. A virtual block not backed by any SELF
// mapping (zero-fill or ancestor-only) gets -1 (sparse). When multiple
// self frames overlap a single block, the worst (lowest) ratio wins.
//
// Frames are indexed in SELF's U-space (storage offsets in the current
// build's data file); we project U→V through every SELF mapping. For any
// mapping m with BuildId == self, the U-range [m.BuildStorageOffset,
// m.BuildStorageOffset+m.Length) corresponds to V-range [m.Offset,
// m.Offset+m.Length).
func compressionPerChunk(h *header.Header, frames []frameInfo, chunkCount int, chunkSize int64) []float64 {
	out := make([]float64, chunkCount)
	for i := range out {
		out[i] = -1
	}
	if h == nil || len(frames) == 0 {
		return out
	}
	selfID := h.Metadata.BuildId
	for _, m := range h.Mapping.All() {
		if m.BuildId != selfID || m.Length == 0 {
			continue
		}
		uStart := int64(m.BuildStorageOffset)
		uEnd := uStart + int64(m.Length)

		// Frames are sorted by StartU; skip to the first one that ends
		// past this mapping's U start.
		fi := sort.Search(len(frames), func(i int) bool {
			return frames[i].EndU > uStart
		})
		for ; fi < len(frames) && frames[fi].StartU < uEnd; fi++ {
			f := frames[fi]
			if f.EndU <= f.StartU || f.EndC <= f.StartC {
				continue
			}
			r := ratio(f.EndU-f.StartU, f.EndC-f.StartC)

			// U-overlap between this mapping and this frame, projected
			// to V-space via the mapping.
			overlapU0 := max(uStart, f.StartU)
			overlapU1 := min(uEnd, f.EndU)
			vStart := int64(m.Offset) + (overlapU0 - uStart)
			vEnd := int64(m.Offset) + (overlapU1 - uStart)

			first := vStart / chunkSize
			last := (vEnd - 1) / chunkSize
			if last >= int64(chunkCount) {
				last = int64(chunkCount - 1)
			}
			for b := first; b <= last; b++ {
				if out[b] < 0 || r < out[b] {
					out[b] = r
				}
			}
		}
	}

	return out
}

// compressionCell picks the char + color for a single block's compression
// ratio. -1 means no frame covers this block (sparse → dimmed dot).
func compressionCell(r float64) string {
	if r < 0 {
		return ansiDim + "·" + ansiReset
	}

	return heatColor(ratioNorm(r)).fg() + "█" + ansiReset
}

// densityCell picks the char + color for a single chunk's count-of-thing
// metric — used by both F (fetches/hugepage) and M (mappings/hugepage).
// 0 (untouched) and 1 (single fetch/mapping → unavoidable, ideal) render as
// a dimmed dot; 2..8+ are colored cool→hot.
func densityCell(n float64) string {
	v := int(n)
	if v <= 1 {
		return ansiDim + "·" + ansiReset
	}
	// 2 → t=0 (green), 8+ → t=1 (red); invert for heatColor.
	t := float64(min(v, 8)-2) / 6

	return heatColor(1-t).fg() + "█" + ansiReset
}

// worstCompression returns the lower of two ratios, treating -1 (no frame)
// as "no worse than" any real ratio — so a covered block dominates a sparse
// block in the downsample bucket.
func worstCompression(a, b float64) float64 {
	switch {
	case a < 0:
		return b
	case b < 0:
		return a
	case a < b:
		return a
	default:
		return b
	}
}

// worstFetch returns the higher segment count (more fetches = worse).
func worstFetch(a, b float64) float64 {
	if a > b {
		return a
	}

	return b
}

// intsToFloat converts a []int to []float64 so framemapRow can take both
// dimensions through one signature.
func intsToFloat(in []int) []float64 {
	out := make([]float64, len(in))
	for i, v := range in {
		out[i] = float64(v)
	}

	return out
}

// heatmap prints pre-rendered cells as a wrapped block.
func heatmap(w io.Writer, cells []string) {
	if len(cells) == 0 {
		return
	}
	var b strings.Builder
	b.WriteString("\n  ")
	for i, c := range cells {
		if i > 0 && i%heatmapWidth == 0 {
			b.WriteString("\n  ")
		}
		b.WriteString(c)
	}
	b.WriteString("\n")
	fmt.Fprintln(w, b.String())
}

// frameCells colors each frame (or group of frames, when over the cell cap)
// by how well it compressed.
func frameCells(frames []frameInfo) []string {
	if len(frames) == 0 {
		return nil
	}
	n := min(len(frames), heatmapCells)
	perCell := (len(frames) + n - 1) / n

	cells := make([]string, 0, n)
	for i := 0; i < len(frames); i += perCell {
		var u, c int64
		for _, f := range frames[i:min(i+perCell, len(frames))] {
			u += f.EndU - f.StartU
			c += f.EndC - f.StartC
		}
		cells = append(cells, heatColor(ratioNorm(ratio(u, c))).fg()+"█"+ansiReset)
	}

	return cells
}

func frameRatio(f frameInfo) float64 { return ratio(f.EndU-f.StartU, f.EndC-f.StartC) }

// ratioNorm maps a compression ratio onto [0,1] for the heat gradient:
// 1x→0 (red, barely compressed), 10x→1 (green, well compressed).
func ratioNorm(r float64) float64 {
	return (r - 1) / 9
}
