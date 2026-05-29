package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/launchdarkly/go-sdk-common/v3/ldlog"
	"go.opentelemetry.io/otel/metric/noop"

	"github.com/e2b-dev/infra/packages/orchestrator/cmd/internal/cmdutil"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/cfg"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block"
	blockmetrics "github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block/metrics"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/build"
	sboxtemplate "github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/template"
	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

const (
	artifactMemfile = "memfile"
	artifactRootfs  = "rootfs"
)

type buildPair struct {
	BuildID               string
	ParentBuildID         string
	SiblingBuildID        string
	SiblingMemfileBuildID string
	SiblingRootfsBuildID  string
	Family                string
}

type pool struct {
	Name              string
	TargetArtifact    string
	CandidateArtifact string
	CandidateBuild    func(buildPair) string
	Positional        bool
	ValidationOnly    bool
}

type rowResult struct {
	BuildID               string
	ParentBuildID         string
	Family                string
	TargetArtifact        string
	Pool                  string
	CandidateBuildID      string
	CandidateArtifact     string
	ValidationOnly        bool
	Positional            bool
	TargetPages           int64
	SampledTargetPages    int64
	CandidatePages        int64
	IndexedCandidatePages int64
	Hits                  int64
	ZeroPages             int64
	EligibleBytes         int64
	SavingsBytes          int64
	SavingsRatio          float64
	FrameSizeBytes        int64
	FrameTargetFrames     int64
	FrameHits             int64
	FrameSavingsBytes     int64
	FrameSavingsRatio     float64
	IndexMS               int64
	CompareMS             int64
	Error                 string
}

type summary struct {
	Rows              int64
	EligibleBytes     int64
	SavingsBytes      int64
	Hits              int64
	TargetPages       int64
	FrameTargetFrames int64
	FrameHits         int64
	Errors            int64
	Ratios            []float64
}

type frameRef struct {
	off    int64
	length int64
}

type analyzer struct {
	ctx       context.Context
	store     storage.StorageProvider
	diffStore *build.DiffStore
	cacheDir  string
	metrics   blockmetrics.Metrics
	devices   map[string]*sboxtemplate.Storage
}

func main() {
	storagePath := flag.String("storage", ".local-build", "storage: local path or gs://bucket")
	buildsFile := flag.String("builds-file", "", "CSV with build_id,parent_build_id and optional sibling columns")
	buildID := flag.String("build", "", "single current build ID")
	parentBuildID := flag.String("parent-build", "", "single parent build ID")
	artifacts := flag.String("artifacts", "both", "memfile, rootfs, or both")
	maxTargetPages := flag.Int("max-target-pages", 50000, "target pages to sample per artifact; 0 scans all")
	maxCandidatePages := flag.Int("max-candidate-pages", 100000, "candidate pages to index per pool; 0 scans all")
	frameSize := flag.Int("frame-size", 2<<20, "compression-frame size for whole-frame dedup estimate")
	seed := flag.Int64("seed", 1, "sampling seed")
	outputPath := flag.String("csv-path", "", "write detailed CSV here; default stdout")
	includeValidation := flag.Bool("include-validation", true, "include validation-only pools")

	flag.Parse()
	cmdutil.SuppressNoisyLogs()

	if *buildsFile == "" && (*buildID == "" || *parentBuildID == "") {
		log.Fatal("provide -builds-file or both -build and -parent-build")
	}

	pairs, err := loadPairs(*buildsFile, *buildID, *parentBuildID)
	if err != nil {
		log.Fatal(err)
	}

	ctx := context.Background()
	if err := cmdutil.SetupStorage(*storagePath); err != nil {
		log.Fatal(err)
	}

	ff, err := featureflags.NewClientWithLogLevel(ldlog.Error)
	if err != nil {
		log.Fatalf("feature flags: %s", err)
	}
	metrics, err := blockmetrics.NewMetrics(noop.NewMeterProvider())
	if err != nil {
		log.Fatalf("metrics: %s", err)
	}
	persistence, err := storage.GetStorageProvider(ctx, storage.TemplateStorageConfig)
	if err != nil {
		log.Fatalf("storage: %s", err)
	}
	cacheDir, err := os.MkdirTemp("", "sample-dedup-gains-*")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(cacheDir)

	diffStore, err := build.NewDiffStore(cfg.Config{}, ff, cacheDir, time.Hour, time.Second)
	if err != nil {
		log.Fatal(err)
	}
	defer diffStore.Close()

	out := io.Writer(os.Stdout)
	var outFile *os.File
	if *outputPath != "" {
		if err := os.MkdirAll(filepath.Dir(*outputPath), 0o755); err != nil {
			log.Fatal(err)
		}
		outFile, err = os.Create(*outputPath)
		if err != nil {
			log.Fatal(err)
		}
		defer outFile.Close()
		out = outFile
	}

	a := &analyzer{
		ctx:       ctx,
		store:     persistence,
		diffStore: diffStore,
		cacheDir:  cacheDir,
		metrics:   metrics,
		devices:   make(map[string]*sboxtemplate.Storage),
	}
	defer a.close()

	writer := csv.NewWriter(out)
	if err := writer.Write(resultHeader()); err != nil {
		log.Fatal(err)
	}

	selectedArtifacts, err := parseArtifacts(*artifacts)
	if err != nil {
		log.Fatal(err)
	}

	summaries := make(map[string]*summary)
	for i, pair := range pairs {
		for _, artifact := range selectedArtifacts {
			results := a.analyzeArtifact(pair, artifact, *maxTargetPages, *maxCandidatePages, int64(*frameSize), *seed+int64(i), *includeValidation)
			for _, result := range results {
				if err := writer.Write(result.Record()); err != nil {
					log.Fatal(err)
				}
				addSummary(summaries, result)
			}
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		log.Fatal(err)
	}

	printSummary(os.Stderr, summaries)
}

func (a *analyzer) analyzeArtifact(pair buildPair, artifact string, maxTargetPages, maxCandidatePages int, frameSize int64, seed int64, includeValidation bool) []rowResult {
	target, err := a.device(pair.BuildID, artifact)
	if err != nil {
		return []rowResult{errorResult(pair, artifact, "open_target", err)}
	}

	targetOffsets, targetPages := sampledSelfPages(target.Header(), pair.BuildID, maxTargetPages, seed)
	results := make([]rowResult, 0)
	for _, p := range candidatePools(pair, artifact, includeValidation) {
		candidateBuildID := p.CandidateBuild(pair)
		base := rowResult{
			BuildID:            pair.BuildID,
			ParentBuildID:      pair.ParentBuildID,
			Family:             pair.Family,
			TargetArtifact:     artifact,
			Pool:               p.Name,
			CandidateBuildID:   candidateBuildID,
			CandidateArtifact:  p.CandidateArtifact,
			ValidationOnly:     p.ValidationOnly,
			Positional:         p.Positional,
			FrameSizeBytes:     frameSize,
			TargetPages:        targetPages,
			SampledTargetPages: int64(len(targetOffsets)),
		}
		if candidateBuildID == "" {
			base.Error = "candidate build missing"
			results = append(results, base)
			continue
		}

		candidate, err := a.device(candidateBuildID, p.CandidateArtifact)
		if err != nil {
			base.Error = err.Error()
			results = append(results, base)
			continue
		}
		if p.Positional {
			results = append(results, a.comparePositional(base, target, candidate, targetOffsets))
			continue
		}
		results = append(results, a.compareIndexed(base, target, candidate, targetOffsets, maxCandidatePages, seed))
	}

	return results
}

func (a *analyzer) comparePositional(base rowResult, target, candidate block.ReadonlyDevice, targetOffsets []int64) rowResult {
	start := time.Now()
	var hits, zeroes int64
	targetPage := make([]byte, header.PageSize)
	candidatePage := make([]byte, header.PageSize)
	for _, off := range targetOffsets {
		if _, err := target.ReadAt(a.ctx, targetPage, off); err != nil {
			base.Error = fmt.Sprintf("read target at %d: %s", off, err)
			break
		}
		if header.IsZero(targetPage) {
			zeroes++
			continue
		}
		if _, err := candidate.ReadAt(a.ctx, candidatePage, off); err != nil {
			continue
		}
		if bytes.Equal(targetPage, candidatePage) {
			hits++
		}
	}
	base.Hits = hits
	base.ZeroPages = zeroes
	base.CompareMS = time.Since(start).Milliseconds()
	base.CandidatePages = base.TargetPages
	base.IndexedCandidatePages = base.SampledTargetPages
	base.measureFramePositional(a.ctx, target, candidate, targetOffsets)
	base.finish()

	return base
}

func (a *analyzer) compareIndexed(base rowResult, target, candidate block.ReadonlyDevice, targetOffsets []int64, maxCandidatePages int, seed int64) rowResult {
	indexStart := time.Now()
	candidateOffsets, candidatePages := sampledAllPages(candidate.Header(), maxCandidatePages, seed)
	base.CandidatePages = candidatePages

	index := make(map[[32]byte][]int64, len(candidateOffsets))
	page := make([]byte, header.PageSize)
	for _, off := range candidateOffsets {
		if _, err := candidate.ReadAt(a.ctx, page, off); err != nil {
			continue
		}
		if header.IsZero(page) {
			continue
		}
		sum := sha256.Sum256(page)
		index[sum] = append(index[sum], off)
		base.IndexedCandidatePages++
	}
	base.IndexMS = time.Since(indexStart).Milliseconds()

	compareStart := time.Now()
	targetPage := make([]byte, header.PageSize)
	candidatePage := make([]byte, header.PageSize)
	var hits, zeroes int64
	for _, off := range targetOffsets {
		if _, err := target.ReadAt(a.ctx, targetPage, off); err != nil {
			base.Error = fmt.Sprintf("read target at %d: %s", off, err)
			break
		}
		if header.IsZero(targetPage) {
			zeroes++
			continue
		}
		for _, candidateOff := range index[sha256.Sum256(targetPage)] {
			if _, err := candidate.ReadAt(a.ctx, candidatePage, candidateOff); err != nil {
				continue
			}
			if bytes.Equal(targetPage, candidatePage) {
				hits++
				break
			}
		}
	}
	base.Hits = hits
	base.ZeroPages = zeroes
	base.CompareMS = time.Since(compareStart).Milliseconds()
	base.measureFrameIndexed(a.ctx, target, candidate, targetOffsets)
	base.finish()

	return base
}

func (r *rowResult) finish() {
	if r.SampledTargetPages == 0 {
		return
	}
	r.EligibleBytes = r.TargetPages * header.PageSize
	r.SavingsBytes = int64(float64(r.Hits) / float64(r.SampledTargetPages) * float64(r.EligibleBytes))
	if r.EligibleBytes > 0 {
		r.SavingsRatio = float64(r.SavingsBytes) / float64(r.EligibleBytes)
	}
	if r.FrameTargetFrames > 0 {
		r.FrameSavingsBytes = r.FrameHits * r.FrameSizeBytes
		r.FrameSavingsRatio = float64(r.FrameHits) / float64(r.FrameTargetFrames)
	}
}

func (r *rowResult) measureFramePositional(ctx context.Context, target, candidate block.ReadonlyDevice, targetOffsets []int64) {
	frames := targetFrames(targetOffsets, r.FrameSizeBytes)
	r.FrameTargetFrames = int64(len(frames))
	if r.FrameSizeBytes <= 0 {
		return
	}

	targetSize, err := target.Size(ctx)
	if err != nil {
		return
	}
	candidateSize, err := candidate.Size(ctx)
	if err != nil {
		return
	}
	for _, off := range frames {
		length := min(r.FrameSizeBytes, targetSize-off)
		if length <= 0 || off+length > candidateSize {
			continue
		}
		targetFrame, err := readRange(ctx, target, off, length)
		if err != nil {
			continue
		}
		candidateFrame, err := readRange(ctx, candidate, off, length)
		if err != nil {
			continue
		}
		if bytes.Equal(targetFrame, candidateFrame) {
			r.FrameHits++
		}
	}
}

func (r *rowResult) measureFrameIndexed(ctx context.Context, target, candidate block.ReadonlyDevice, targetOffsets []int64) {
	frames := targetFrames(targetOffsets, r.FrameSizeBytes)
	r.FrameTargetFrames = int64(len(frames))
	if r.FrameSizeBytes <= 0 {
		return
	}

	candidateSize, err := candidate.Size(ctx)
	if err != nil {
		return
	}
	index := make(map[[32]byte][]frameRef)
	for off := int64(0); off < candidateSize; off += r.FrameSizeBytes {
		length := min(r.FrameSizeBytes, candidateSize-off)
		frame, err := readRange(ctx, candidate, off, length)
		if err != nil || header.IsZero(frame) {
			continue
		}
		index[sha256.Sum256(frame)] = append(index[sha256.Sum256(frame)], frameRef{off: off, length: length})
	}

	targetSize, err := target.Size(ctx)
	if err != nil {
		return
	}
	for _, off := range frames {
		length := min(r.FrameSizeBytes, targetSize-off)
		if length <= 0 {
			continue
		}
		targetFrame, err := readRange(ctx, target, off, length)
		if err != nil || header.IsZero(targetFrame) {
			continue
		}
		sum := sha256.Sum256(targetFrame)
		for _, ref := range index[sum] {
			if ref.length != length {
				continue
			}
			candidateFrame, err := readRange(ctx, candidate, ref.off, ref.length)
			if err != nil {
				continue
			}
			if bytes.Equal(targetFrame, candidateFrame) {
				r.FrameHits++
				break
			}
		}
	}
}

func targetFrames(targetOffsets []int64, frameSize int64) []int64 {
	if frameSize <= 0 {
		return nil
	}
	seen := make(map[int64]struct{})
	frames := make([]int64, 0)
	for _, off := range targetOffsets {
		frameOff := (off / frameSize) * frameSize
		if _, ok := seen[frameOff]; ok {
			continue
		}
		seen[frameOff] = struct{}{}
		frames = append(frames, frameOff)
	}

	return frames
}

func readRange(ctx context.Context, d block.ReadonlyDevice, off, length int64) ([]byte, error) {
	buf := make([]byte, length)
	_, err := d.ReadAt(ctx, buf, off)

	return buf, err
}

func (a *analyzer) device(buildID, artifact string) (*sboxtemplate.Storage, error) {
	key := buildID + "/" + artifact
	if d, ok := a.devices[key]; ok {
		return d, nil
	}
	fileType, err := diffType(artifact)
	if err != nil {
		return nil, err
	}
	d, err := sboxtemplate.NewStorage(a.ctx, a.diffStore, buildID, fileType, nil, a.store, a.metrics)
	if err != nil {
		return nil, err
	}
	a.devices[key] = d

	return d, nil
}

func (a *analyzer) close() {
	for _, d := range a.devices {
		_ = d.Close()
	}
}

func sampledSelfPages(h *header.Header, buildID string, maxPages int, seed int64) ([]int64, int64) {
	id, err := uuid.Parse(buildID)
	if err != nil || h == nil {
		return nil, 0
	}
	var total int64
	return sampleOffsets(h.Mapping, func(m header.BuildMap) bool {
		return m.BuildId == id
	}, maxPages, seed, &total), total
}

func sampledAllPages(h *header.Header, maxPages int, seed int64) ([]int64, int64) {
	if h == nil || h.Metadata == nil {
		return nil, 0
	}
	total := int64(h.Metadata.Size / header.PageSize)
	return sampleOffsets([]header.BuildMap{{
		Offset: 0,
		Length: uint64(total * header.PageSize),
	}}, func(header.BuildMap) bool { return true }, maxPages, seed, nil), total
}

func sampleOffsets(mappings []header.BuildMap, include func(header.BuildMap) bool, maxPages int, seed int64, totalOut *int64) []int64 {
	rng := rand.New(rand.NewSource(seed))
	var sampled []int64
	var seen int64
	for _, m := range mappings {
		if !include(m) {
			continue
		}
		start := alignUp(int64(m.Offset), header.PageSize)
		end := alignDown(int64(m.Offset+m.Length), header.PageSize)
		for off := start; off < end; off += header.PageSize {
			seen++
			if maxPages <= 0 {
				sampled = append(sampled, off)
				continue
			}
			if int64(len(sampled)) < int64(maxPages) {
				sampled = append(sampled, off)
				continue
			}
			j := rng.Int63n(seen)
			if j < int64(maxPages) {
				sampled[j] = off
			}
		}
	}
	if totalOut != nil {
		*totalOut = seen
	}

	return sampled
}

func alignUp(v, by int64) int64 {
	if v%by == 0 {
		return v
	}
	return v + by - v%by
}

func alignDown(v, by int64) int64 {
	return v - v%by
}

func candidatePools(pair buildPair, targetArtifact string, includeValidation bool) []pool {
	siblingFor := func(artifact string) func(buildPair) string {
		return func(p buildPair) string {
			if artifact == artifactMemfile && p.SiblingMemfileBuildID != "" {
				return p.SiblingMemfileBuildID
			}
			if artifact == artifactRootfs && p.SiblingRootfsBuildID != "" {
				return p.SiblingRootfsBuildID
			}
			return p.SiblingBuildID
		}
	}
	parent := func(p buildPair) string { return p.ParentBuildID }
	current := func(p buildPair) string { return p.BuildID }

	var pools []pool
	switch targetArtifact {
	case artifactMemfile:
		pools = append(pools,
			pool{Name: "memfile_parent_memfile_positional", TargetArtifact: artifactMemfile, CandidateArtifact: artifactMemfile, CandidateBuild: parent, Positional: true},
			pool{Name: "memfile_current_rootfs", TargetArtifact: artifactMemfile, CandidateArtifact: artifactRootfs, CandidateBuild: current},
			pool{Name: "memfile_sibling_memfile", TargetArtifact: artifactMemfile, CandidateArtifact: artifactMemfile, CandidateBuild: siblingFor(artifactMemfile)},
		)
		if includeValidation {
			pools = append(pools,
				pool{Name: "memfile_parent_rootfs", TargetArtifact: artifactMemfile, CandidateArtifact: artifactRootfs, CandidateBuild: parent, ValidationOnly: true},
				pool{Name: "memfile_sibling_rootfs", TargetArtifact: artifactMemfile, CandidateArtifact: artifactRootfs, CandidateBuild: siblingFor(artifactRootfs), ValidationOnly: true},
			)
		}
	case artifactRootfs:
		pools = append(pools,
			pool{Name: "rootfs_parent_rootfs_positional", TargetArtifact: artifactRootfs, CandidateArtifact: artifactRootfs, CandidateBuild: parent, Positional: true},
			pool{Name: "rootfs_parent_memfile", TargetArtifact: artifactRootfs, CandidateArtifact: artifactMemfile, CandidateBuild: parent},
			pool{Name: "rootfs_sibling_rootfs", TargetArtifact: artifactRootfs, CandidateArtifact: artifactRootfs, CandidateBuild: siblingFor(artifactRootfs)},
		)
		if includeValidation {
			pools = append(pools,
				pool{Name: "rootfs_current_memfile", TargetArtifact: artifactRootfs, CandidateArtifact: artifactMemfile, CandidateBuild: current, ValidationOnly: true},
				pool{Name: "rootfs_sibling_memfile", TargetArtifact: artifactRootfs, CandidateArtifact: artifactMemfile, CandidateBuild: siblingFor(artifactMemfile), ValidationOnly: true},
			)
		}
	}

	return pools
}

func parseArtifacts(value string) ([]string, error) {
	switch strings.ToLower(value) {
	case "both":
		return []string{artifactMemfile, artifactRootfs}, nil
	case artifactMemfile:
		return []string{artifactMemfile}, nil
	case artifactRootfs:
		return []string{artifactRootfs}, nil
	default:
		return nil, fmt.Errorf("unknown -artifacts value %q", value)
	}
}

func diffType(artifact string) (build.DiffType, error) {
	switch artifact {
	case artifactMemfile:
		return build.Memfile, nil
	case artifactRootfs:
		return build.Rootfs, nil
	default:
		return "", fmt.Errorf("unknown artifact %q", artifact)
	}
}

func loadPairs(path, buildID, parentBuildID string) ([]buildPair, error) {
	if path == "" {
		return []buildPair{{BuildID: buildID, ParentBuildID: parentBuildID}}, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	r := csv.NewReader(f)
	r.TrimLeadingSpace = true
	records, err := r.ReadAll()
	if err != nil {
		return nil, err
	}
	if len(records) == 0 {
		return nil, errors.New("builds file is empty")
	}

	start := 0
	index := map[string]int{}
	if looksLikeHeader(records[0]) {
		start = 1
		for i, name := range records[0] {
			index[strings.TrimSpace(name)] = i
		}
	}

	pairs := make([]buildPair, 0, len(records)-start)
	for _, record := range records[start:] {
		pair := buildPair{
			BuildID:               getCSV(record, index, "build_id", 0),
			ParentBuildID:         getCSV(record, index, "parent_build_id", 1),
			SiblingBuildID:        getCSV(record, index, "sibling_build_id", 2),
			SiblingMemfileBuildID: getCSV(record, index, "sibling_memfile_build_id", -1),
			SiblingRootfsBuildID:  getCSV(record, index, "sibling_rootfs_build_id", -1),
			Family:                getCSV(record, index, "family", -1),
		}
		if pair.BuildID == "" || pair.ParentBuildID == "" {
			return nil, fmt.Errorf("build_id and parent_build_id are required in record %q", strings.Join(record, ","))
		}
		pairs = append(pairs, pair)
	}

	return pairs, nil
}

func looksLikeHeader(record []string) bool {
	for _, field := range record {
		if strings.Contains(strings.ToLower(field), "build") {
			return true
		}
	}
	return false
}

func getCSV(record []string, index map[string]int, name string, fallback int) string {
	if i, ok := index[name]; ok && i >= 0 && i < len(record) {
		return strings.TrimSpace(record[i])
	}
	if fallback >= 0 && fallback < len(record) {
		return strings.TrimSpace(record[fallback])
	}
	return ""
}

func resultHeader() []string {
	return []string{
		"build_id", "parent_build_id", "family", "target_artifact", "pool",
		"candidate_build_id", "candidate_artifact", "validation_only", "positional",
		"target_pages", "sampled_target_pages", "candidate_pages", "indexed_candidate_pages",
		"hits", "zero_pages", "eligible_bytes", "savings_bytes", "savings_ratio",
		"frame_size_bytes", "frame_target_frames", "frame_hits", "frame_savings_bytes", "frame_savings_ratio",
		"index_ms", "compare_ms", "error",
	}
}

func (r rowResult) Record() []string {
	return []string{
		r.BuildID, r.ParentBuildID, r.Family, r.TargetArtifact, r.Pool,
		r.CandidateBuildID, r.CandidateArtifact,
		strconv.FormatBool(r.ValidationOnly), strconv.FormatBool(r.Positional),
		strconv.FormatInt(r.TargetPages, 10),
		strconv.FormatInt(r.SampledTargetPages, 10),
		strconv.FormatInt(r.CandidatePages, 10),
		strconv.FormatInt(r.IndexedCandidatePages, 10),
		strconv.FormatInt(r.Hits, 10),
		strconv.FormatInt(r.ZeroPages, 10),
		strconv.FormatInt(r.EligibleBytes, 10),
		strconv.FormatInt(r.SavingsBytes, 10),
		strconv.FormatFloat(r.SavingsRatio, 'f', 6, 64),
		strconv.FormatInt(r.FrameSizeBytes, 10),
		strconv.FormatInt(r.FrameTargetFrames, 10),
		strconv.FormatInt(r.FrameHits, 10),
		strconv.FormatInt(r.FrameSavingsBytes, 10),
		strconv.FormatFloat(r.FrameSavingsRatio, 'f', 6, 64),
		strconv.FormatInt(r.IndexMS, 10),
		strconv.FormatInt(r.CompareMS, 10),
		r.Error,
	}
}

func errorResult(pair buildPair, artifact, pool string, err error) rowResult {
	return rowResult{
		BuildID:        pair.BuildID,
		ParentBuildID:  pair.ParentBuildID,
		Family:         pair.Family,
		TargetArtifact: artifact,
		Pool:           pool,
		Error:          err.Error(),
	}
}

func addSummary(summaries map[string]*summary, r rowResult) {
	s := summaries[r.Pool]
	if s == nil {
		s = &summary{}
		summaries[r.Pool] = s
	}
	s.Rows++
	s.EligibleBytes += r.EligibleBytes
	s.SavingsBytes += r.SavingsBytes
	s.Hits += r.Hits
	s.TargetPages += r.SampledTargetPages
	s.FrameTargetFrames += r.FrameTargetFrames
	s.FrameHits += r.FrameHits
	if r.Error != "" {
		s.Errors++
	}
	if r.Error == "" && r.EligibleBytes > 0 {
		s.Ratios = append(s.Ratios, r.SavingsRatio)
	}
}

func printSummary(w io.Writer, summaries map[string]*summary) {
	fmt.Fprintln(w, "\nSUMMARY")
	fmt.Fprintln(w, "pool,rows,errors,sampled_pages,hits,eligible_bytes,savings_bytes,weighted_savings_ratio,frame_target_frames,frame_hits,frame_savings_ratio,mean_row_ratio,ci95_low,ci95_high")
	for pool, s := range summaries {
		ratio := 0.0
		if s.EligibleBytes > 0 {
			ratio = float64(s.SavingsBytes) / float64(s.EligibleBytes)
		}
		frameRatio := 0.0
		if s.FrameTargetFrames > 0 {
			frameRatio = float64(s.FrameHits) / float64(s.FrameTargetFrames)
		}
		mean, low, high := bootstrapMeanCI(s.Ratios, 1000, 1)
		fmt.Fprintf(w, "%s,%d,%d,%d,%d,%d,%d,%.6f,%d,%d,%.6f,%.6f,%.6f,%.6f\n",
			pool, s.Rows, s.Errors, s.TargetPages, s.Hits, s.EligibleBytes, s.SavingsBytes, ratio, s.FrameTargetFrames, s.FrameHits, frameRatio, mean, low, high)
	}
}

func bootstrapMeanCI(values []float64, iterations int, seed int64) (mean, low, high float64) {
	if len(values) == 0 {
		return 0, 0, 0
	}
	for _, v := range values {
		mean += v
	}
	mean /= float64(len(values))
	if len(values) == 1 {
		return mean, mean, mean
	}

	rng := rand.New(rand.NewSource(seed))
	samples := make([]float64, iterations)
	for i := range samples {
		var total float64
		for range values {
			total += values[rng.Intn(len(values))]
		}
		samples[i] = total / float64(len(values))
	}
	sortFloat64s(samples)

	return mean, samples[iterations*25/1000], samples[iterations*975/1000]
}

func sortFloat64s(values []float64) {
	for i := 1; i < len(values); i++ {
		v := values[i]
		j := i - 1
		for ; j >= 0 && values[j] > v; j-- {
			values[j+1] = values[j]
		}
		values[j+1] = v
	}
}
