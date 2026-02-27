package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/e2b-dev/infra/packages/orchestrator/cmd/internal/cmdutil"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

// filePartWriter implements storage.PartUploader for local file writes.
type filePartWriter struct {
	path string
	f    *os.File
}

func (w *filePartWriter) Start(_ context.Context) error {
	dir := filepath.Dir(w.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	f, err := os.Create(w.path)
	if err != nil {
		return err
	}
	w.f = f

	return nil
}

func (w *filePartWriter) UploadPart(_ context.Context, _ int, data ...[]byte) error {
	for _, d := range data {
		if _, err := w.f.Write(d); err != nil {
			return err
		}
	}

	return nil
}

func (w *filePartWriter) Complete(_ context.Context) error {
	return w.f.Close()
}

// compressConfig holds the flags for a compression run.
type compressConfig struct {
	storagePath string
	compType    storage.CompressionType
	level       int
	frameSize   int
	maxFrameU   int
	dryRun      bool
	recursive   bool
	verbose     bool
}

func main() {
	build := flag.String("build", "", "build ID")
	template := flag.String("template", "", "template ID or alias (requires E2B_API_KEY)")
	storagePath := flag.String("storage", ".local-build", "storage: local path or gs://bucket")
	compression := flag.String("compression", "lz4", "compression type: lz4 or zstd")
	level := flag.Int("level", storage.DefaultCompressionOptions.Level, "compression level (0=default)")
	frameSize := flag.Int("frame-size", storage.DefaultCompressionOptions.TargetFrameSize, "target compressed frame size in bytes")
	maxFrameU := flag.Int("max-frame-u", storage.DefaultMaxFrameUncompressedSize, "max uncompressed bytes per frame")
	dryRun := flag.Bool("dry-run", false, "show what would be done without making changes")
	recursive := flag.Bool("recursive", false, "recursively compress dependencies (referenced builds)")
	verbose := flag.Bool("v", false, "verbose: print per-frame info during compression")

	flag.Parse()

	// Resolve build ID from template if provided
	if *template != "" && *build != "" {
		log.Fatal("specify either -build or -template, not both")
	}
	if *template != "" {
		resolvedBuild, err := resolveTemplateID(*template)
		if err != nil {
			log.Fatalf("failed to resolve template: %s", err)
		}
		*build = resolvedBuild
		fmt.Printf("Resolved template %q to build %s\n", *template, *build)
	}

	if *build == "" {
		printUsage()
		os.Exit(1)
	}

	// Parse compression type
	var compType storage.CompressionType
	switch *compression {
	case "lz4":
		compType = storage.CompressionLZ4
	case "zstd":
		compType = storage.CompressionZstd
	default:
		log.Fatalf("unsupported compression type: %s (use 'lz4' or 'zstd')", *compression)
	}

	cfg := &compressConfig{
		storagePath: *storagePath,
		compType:    compType,
		level:       *level,
		frameSize:   *frameSize,
		maxFrameU:   *maxFrameU,
		dryRun:      *dryRun,
		recursive:   *recursive,
		verbose:     *verbose,
	}

	ctx := context.Background()

	if err := compressBuild(ctx, cfg, *build, nil); err != nil {
		log.Fatalf("failed to compress build %s: %s", *build, err)
	}

	fmt.Printf("\nDone.\n")
}

func printUsage() {
	fmt.Fprintf(os.Stderr, "Usage: compress-build (-build <uuid> | -template <id-or-alias>) [-storage <path>] [-compression lz4|zstd] [-level N] [-frame-size N] [-dry-run] [-recursive]\n\n")
	fmt.Fprintf(os.Stderr, "Compresses uncompressed build artifacts and creates v4 headers.\n\n")
	fmt.Fprintf(os.Stderr, "The -template flag requires E2B_API_KEY environment variable.\n")
	fmt.Fprintf(os.Stderr, "Set E2B_DOMAIN for non-production environments.\n\n")
	fmt.Fprintf(os.Stderr, "Examples:\n")
	fmt.Fprintf(os.Stderr, "  compress-build -build abc123                              # compress with default LZ4\n")
	fmt.Fprintf(os.Stderr, "  compress-build -build abc123 -compression zstd            # compress with zstd\n")
	fmt.Fprintf(os.Stderr, "  compress-build -build abc123 -dry-run                     # show what would be done\n")
	fmt.Fprintf(os.Stderr, "  compress-build -build abc123 -storage gs://my-bucket      # compress from GCS\n")
	fmt.Fprintf(os.Stderr, "  compress-build -build abc123 -recursive                   # compress build and all dependencies\n")
	fmt.Fprintf(os.Stderr, "  compress-build -template base -storage gs://bucket         # compress by template alias\n")
	fmt.Fprintf(os.Stderr, "  compress-build -template gtjfpksmxd9ct81x1f8e             # compress by template ID\n")
}

// compressBuild compresses a single build and optionally its dependencies.
// visited tracks already-processed builds to avoid cycles.
func compressBuild(ctx context.Context, cfg *compressConfig, buildID string, visited map[string]bool) error {
	if visited == nil {
		visited = make(map[string]bool)
	}
	if visited[buildID] {
		return nil
	}
	visited[buildID] = true

	artifacts := []struct {
		name string
		file string
	}{
		{"memfile", storage.MemfileName},
		{"rootfs", storage.RootfsName},
	}

	// In recursive mode, first discover and compress dependencies.
	if cfg.recursive {
		deps, err := findDependencies(ctx, cfg.storagePath, buildID)
		if err != nil {
			fmt.Printf("  Warning: could not discover dependencies for %s: %s\n", buildID, err)
		} else if len(deps) > 0 {
			fmt.Printf("\nBuild %s has %d dependency build(s): %s\n", buildID, len(deps), strings.Join(deps, ", "))
			for _, depBuild := range deps {
				// Check if the dependency already has compressed data.
				alreadyCompressed := true
				for _, a := range artifacts {
					compressedFile := storage.V4DataName(a.file, cfg.compType)
					info := cmdutil.ProbeFile(ctx, cfg.storagePath, depBuild, compressedFile)
					if !info.Exists {
						alreadyCompressed = false

						break
					}
				}
				if alreadyCompressed {
					fmt.Printf("  Dependency %s already compressed, skipping\n", depBuild)

					continue
				}

				fmt.Printf("\n>>> Compressing dependency %s\n", depBuild)
				if err := compressBuild(ctx, cfg, depBuild, visited); err != nil {
					return fmt.Errorf("dependency %s: %w", depBuild, err)
				}
			}
		}
	}

	fmt.Printf("\n====== Build %s ======\n", buildID)

	for _, artifact := range artifacts {
		if err := compressArtifact(ctx, cfg, buildID, artifact.name, artifact.file); err != nil {
			return fmt.Errorf("failed to compress %s: %w", artifact.name, err)
		}
	}

	return nil
}

// findDependencies reads headers for a build and returns unique build IDs
// referenced in mappings (excluding the build itself and nil UUIDs).
func findDependencies(ctx context.Context, storagePath, buildID string) ([]string, error) {
	seen := make(map[string]bool)

	for _, file := range []string{storage.MemfileName, storage.RootfsName} {
		headerFile := file + storage.HeaderSuffix
		headerData, _, err := cmdutil.ReadFileIfExists(ctx, storagePath, buildID, headerFile)
		if err != nil {
			return nil, fmt.Errorf("read header %s: %w", headerFile, err)
		}
		if headerData == nil {
			continue
		}

		h, err := header.DeserializeBytes(headerData)
		if err != nil {
			return nil, fmt.Errorf("deserialize %s: %w", headerFile, err)
		}

		for _, m := range h.Mapping {
			bid := m.BuildId.String()
			if bid != buildID && bid != cmdutil.NilUUID {
				seen[bid] = true
			}
		}
	}

	deps := make([]string, 0, len(seen))
	for bid := range seen {
		deps = append(deps, bid)
	}

	return deps, nil
}

func compressArtifact(ctx context.Context, cfg *compressConfig, buildID, name, file string) error {
	fmt.Printf("\n=== %s ===\n", name)

	// Read uncompressed header
	headerFile := file + storage.HeaderSuffix
	headerData, _, err := cmdutil.ReadFile(ctx, cfg.storagePath, buildID, headerFile)
	if err != nil {
		return fmt.Errorf("read header: %w", err)
	}

	h, err := header.DeserializeBytes(headerData)
	if err != nil {
		return fmt.Errorf("deserialize header: %w", err)
	}
	fmt.Printf("  Header: version=%d, mappings=%d, size=%#x\n",
		h.Metadata.Version, len(h.Mapping), h.Metadata.Size)

	// Check if compressed data already exists
	compressedFile := storage.V4DataName(file, cfg.compType)
	existing := cmdutil.ProbeFile(ctx, cfg.storagePath, buildID, compressedFile)
	if existing.Exists {
		fmt.Printf("  Compressed file already exists: %s (%#x), skipping\n", existing.Path, existing.Size)

		return nil
	}

	// Check if v4 header already exists
	compressedHeaderFile := storage.V4HeaderName(file)
	existingHeader := cmdutil.ProbeFile(ctx, cfg.storagePath, buildID, compressedHeaderFile)
	if existingHeader.Exists {
		fmt.Printf("  Compressed header already exists: %s (%#x), skipping\n", existingHeader.Path, existingHeader.Size)

		return nil
	}

	if cfg.dryRun {
		fmt.Printf("  [dry-run] Would compress %s -> %s\n", file, compressedFile)
		fmt.Printf("  [dry-run] Would create compressed header -> %s\n", compressedHeaderFile)

		return nil
	}

	// Open data file for reading
	reader, dataSize, dataSource, err := cmdutil.OpenDataFile(ctx, cfg.storagePath, buildID, file)
	if err != nil {
		return fmt.Errorf("open data file: %w", err)
	}
	defer reader.Close()

	fmt.Printf("  Data: %s (%#x, %.1f MiB)\n", dataSource, dataSize, float64(dataSize)/1024/1024)

	// Set up compression options
	opts := &storage.FramedUploadOptions{
		CompressionType:          cfg.compType,
		Level:                    cfg.level,
		TargetFrameSize:          cfg.frameSize,
		MaxUncompressedFrameSize: cfg.maxFrameU,
		TargetPartSize:           50 * 1024 * 1024,
	}

	if cfg.verbose {
		frameIdx := 0
		lastFrameTime := time.Now()
		opts.OnFrameReady = func(offset storage.FrameOffset, size storage.FrameSize, _ []byte) error {
			now := time.Now()
			elapsed := now.Sub(lastFrameTime)
			mbps := float64(size.U) / elapsed.Seconds() / (1024 * 1024)
			lastFrameTime = now
			ratio := float64(size.U) / float64(size.C)
			fmt.Printf("    frame[%d] U=%#x+%#x C=%#x+%#x ratio=%s %v %.0f MB/s\n",
				frameIdx, offset.U, size.U, offset.C, size.C,
				cmdutil.FormatRatio(ratio), elapsed.Round(time.Millisecond), mbps)
			frameIdx++

			return nil
		}
	}

	// Compress to a temp file, then upload if GCS
	tmpDir, err := os.MkdirTemp("", "compress-build-*")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	tmpCompressedPath := filepath.Join(tmpDir, compressedFile)
	uploader := &filePartWriter{path: tmpCompressedPath}

	// Create an io.Reader from the DataReader (which supports ReadAt)
	sectionReader := io.NewSectionReader(reader, 0, dataSize)

	fmt.Printf("  Compressing with %s (level=%d, frame-size=%#x, max-frame-u=%#x)...\n",
		cfg.compType, cfg.level, cfg.frameSize, cfg.maxFrameU)

	// Compress
	compressStart := time.Now()
	frameTable, err := storage.CompressStream(ctx, sectionReader, opts, uploader)
	if err != nil {
		return fmt.Errorf("compress: %w", err)
	}
	compressElapsed := time.Since(compressStart)

	// Print compression stats
	var totalU, totalC int64
	for _, f := range frameTable.Frames {
		totalU += int64(f.U)
		totalC += int64(f.C)
	}
	ratio := float64(totalU) / float64(totalC)
	savings := 100.0 * (1.0 - float64(totalC)/float64(totalU))
	mbps := float64(totalU) / compressElapsed.Seconds() / (1024 * 1024)
	fmt.Printf("  Compressed: %d frames, U=%#x C=%#x ratio=%s savings=%.1f%% in %v (%.0f MB/s)\n",
		len(frameTable.Frames), totalU, totalC, cmdutil.FormatRatio(ratio), savings,
		compressElapsed.Round(time.Millisecond), mbps)

	// Apply frame tables to header (current build's own data)
	h.AddFrames(frameTable)

	// Propagate FrameTables from compressed dependencies into this header.
	// Without this, mappings referencing parent builds would have nil FrameTable,
	// forcing uncompressed chunkers for those layers even though compressed data exists.
	propagateDependencyFrames(ctx, cfg.storagePath, h, file)

	h.Metadata.Version = header.MetadataVersionCompressed

	// Serialize as v4
	headerBytes, err := header.Serialize(h.Metadata, h.Mapping)
	if err != nil {
		return fmt.Errorf("serialize v4 header: %w", err)
	}

	// LZ4-block-compress the header
	compressedHeaderBytes, err := storage.CompressLZ4(headerBytes)
	if err != nil {
		return fmt.Errorf("LZ4-compress header: %w", err)
	}

	// Write compressed header to temp
	tmpHeaderPath := filepath.Join(tmpDir, compressedHeaderFile)
	if err := os.WriteFile(tmpHeaderPath, compressedHeaderBytes, 0o644); err != nil {
		return fmt.Errorf("write compressed header: %w", err)
	}

	// Upload to destination
	if cmdutil.IsGCSPath(cfg.storagePath) {
		gcsBase := cmdutil.NormalizeGCSPath(cfg.storagePath) + "/" + buildID + "/"

		fmt.Printf("  Uploading compressed data to %s%s...\n", gcsBase, compressedFile)
		if err := gcloudCopy(ctx, tmpCompressedPath, gcsBase+compressedFile, map[string]string{
			"uncompressed-size": strconv.FormatInt(dataSize, 10),
		}); err != nil {
			return fmt.Errorf("upload compressed data: %w", err)
		}

		fmt.Printf("  Uploading compressed header to %s%s...\n", gcsBase, compressedHeaderFile)
		if err := gcloudCopy(ctx, tmpHeaderPath, gcsBase+compressedHeaderFile, nil); err != nil {
			return fmt.Errorf("upload compressed header: %w", err)
		}
	} else {
		// Local storage: move from temp to final location
		localBase := filepath.Join(cfg.storagePath, "templates", buildID)
		if err := os.MkdirAll(localBase, 0o755); err != nil {
			return fmt.Errorf("mkdir: %w", err)
		}

		finalCompressed := filepath.Join(localBase, compressedFile)
		if err := os.Rename(tmpCompressedPath, finalCompressed); err != nil {
			return fmt.Errorf("move compressed data: %w", err)
		}
		fmt.Printf("  Output: %s\n", finalCompressed)

		// Write uncompressed-size sidecar for local storage
		sidecarPath := finalCompressed + ".uncompressed-size"
		if err := os.WriteFile(sidecarPath, []byte(strconv.FormatInt(dataSize, 10)), 0o644); err != nil {
			return fmt.Errorf("write uncompressed-size sidecar: %w", err)
		}

		finalHeader := filepath.Join(localBase, compressedHeaderFile)
		if err := os.Rename(tmpHeaderPath, finalHeader); err != nil {
			return fmt.Errorf("move compressed header: %w", err)
		}
		fmt.Printf("  Compressed header: %s\n", finalHeader)
	}

	fmt.Printf("  Compressed header: %#x (uncompressed: %#x)\n",
		len(compressedHeaderBytes), len(headerBytes))

	return nil
}

// propagateDependencyFrames reads compressed headers for dependency builds
// and injects their FrameTables into the current header's dependency mappings.
//
// When a derived template references base build data, the header mappings for
// those base builds initially have nil FrameTable. If the base build was
// previously compressed (has a v4 header), we read its FrameTable
// and apply it to the matching mappings in this header. This ensures the
// orchestrator creates compressed chunkers for ALL layers, not just the current build.
func propagateDependencyFrames(ctx context.Context, storagePath string, h *header.Header, artifactFile string) {
	currentBuildID := h.Metadata.BuildId.String()

	// Collect unique dependency build IDs that have nil FrameTable.
	depBuilds := make(map[string]bool)
	for _, m := range h.Mapping {
		bid := m.BuildId.String()
		if bid == currentBuildID || bid == cmdutil.NilUUID {
			continue
		}
		if m.FrameTable == nil {
			depBuilds[bid] = true
		}
	}

	if len(depBuilds) == 0 {
		return
	}

	for depBuild := range depBuilds {
		depH, _, err := cmdutil.ReadCompressedHeader(ctx, storagePath, depBuild, artifactFile)
		if err != nil {
			fmt.Printf("  Warning: could not read compressed header for dependency %s: %s\n", depBuild, err)

			continue
		}
		if depH == nil {
			fmt.Printf("  Warning: no compressed header found for dependency %s (not compressed yet?)\n", depBuild)

			continue
		}

		// Reconstruct the full FrameTable for the dependency by collecting
		// all FrameTables from the dependency's own mappings and merging them.
		fullFT := reconstructFullFrameTable(depH, depBuild)
		if fullFT == nil {
			fmt.Printf("  Warning: dependency %s compressed header has no FrameTable for its own data\n", depBuild)

			continue
		}

		// Apply the full FrameTable to matching mappings in the current header.
		applied := 0
		for _, m := range h.Mapping {
			if m.BuildId.String() != depBuild || m.FrameTable != nil {
				continue
			}
			if err := m.AddFrames(fullFT); err != nil {
				fmt.Printf("  Warning: could not apply frames for dependency %s mapping at offset %#x: %s\n",
					depBuild, m.Offset, err)

				continue
			}
			applied++
		}
		if applied > 0 {
			fmt.Printf("  Propagated %d FrameTable(s) from dependency %s (%d frames, %s)\n",
				applied, depBuild, len(fullFT.Frames), fullFT.CompressionType)
		}
	}
}

// reconstructFullFrameTable merges all per-mapping FrameTables for a given
// build ID from a header into a single FrameTable covering the entire data file.
func reconstructFullFrameTable(h *header.Header, buildID string) *storage.FrameTable {
	var result *storage.FrameTable

	for _, m := range h.Mapping {
		if m.BuildId.String() != buildID || m.FrameTable == nil {
			continue
		}

		ft := m.FrameTable
		if result == nil {
			// First FrameTable — start with a copy
			result = &storage.FrameTable{
				CompressionType: ft.CompressionType,
				StartAt:         ft.StartAt,
				Frames:          make([]storage.FrameSize, len(ft.Frames)),
			}
			copy(result.Frames, ft.Frames)

			continue
		}

		// Extend: calculate where the current result ends (uncompressed offset).
		resultEndU := result.StartAt.U
		for _, f := range result.Frames {
			resultEndU += int64(f.U)
		}

		// Append non-overlapping frames from ft.
		ftCurrentU := ft.StartAt.U
		for _, f := range ft.Frames {
			frameEndU := ftCurrentU + int64(f.U)
			if frameEndU <= resultEndU {
				// Already covered
				ftCurrentU = frameEndU

				continue
			}
			if ftCurrentU < resultEndU {
				// Overlapping frame — same physical frame, skip it
				ftCurrentU = frameEndU

				continue
			}
			// New frame beyond what we have
			result.Frames = append(result.Frames, f)
			ftCurrentU = frameEndU
		}
	}

	return result
}

func gcloudCopy(ctx context.Context, localPath, gcsPath string, metadata map[string]string) error {
	cmd := exec.CommandContext(ctx, "gcloud", "storage", "cp", "--verbosity", "error", localPath, gcsPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("gcloud storage cp failed: %w\n%s", err, string(output))
	}

	// Set custom metadata separately — gcloud storage cp --custom-metadata
	// doesn't work with parallel composite uploads for large files.
	if len(metadata) > 0 {
		pairs := make([]string, 0, len(metadata))
		for k, v := range metadata {
			pairs = append(pairs, k+"="+v)
		}
		updateCmd := exec.CommandContext(ctx, "gcloud", "storage", "objects", "update",
			"--custom-metadata="+strings.Join(pairs, ","), gcsPath)
		updateOutput, updateErr := updateCmd.CombinedOutput()
		if updateErr != nil {
			return fmt.Errorf("gcloud storage objects update failed: %w\n%s", updateErr, string(updateOutput))
		}
	}

	return nil
}

// templateInfo represents a template from the E2B API.
type templateInfo struct {
	TemplateID string   `json:"templateID"`
	BuildID    string   `json:"buildID"`
	Aliases    []string `json:"aliases"`
	Names      []string `json:"names"`
}

// resolveTemplateID fetches the build ID for a template from the E2B API.
func resolveTemplateID(input string) (string, error) {
	apiKey := os.Getenv("E2B_API_KEY")
	if apiKey == "" {
		return "", fmt.Errorf("E2B_API_KEY environment variable required for -template flag")
	}

	apiURL := "https://api.e2b.dev/templates"
	if domain := os.Getenv("E2B_DOMAIN"); domain != "" {
		apiURL = fmt.Sprintf("https://api.%s/templates", domain)
	}

	ctx := context.Background()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("X-API-Key", apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to fetch templates: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)

		return "", fmt.Errorf("API returned %d: %s", resp.StatusCode, string(body))
	}

	var templates []templateInfo
	if err := json.NewDecoder(resp.Body).Decode(&templates); err != nil {
		return "", fmt.Errorf("failed to parse API response: %w", err)
	}

	var match *templateInfo
	var availableAliases []string

	for i := range templates {
		t := &templates[i]
		availableAliases = append(availableAliases, t.Aliases...)

		if t.TemplateID == input {
			match = t

			break
		}

		if slices.Contains(t.Aliases, input) {
			match = t

			break
		}

		if slices.Contains(t.Names, input) {
			match = t

			break
		}
	}

	if match == nil {
		return "", fmt.Errorf("template %q not found. Available aliases: %s", input, strings.Join(availableAliases, ", "))
	}

	if match.BuildID == "" || match.BuildID == cmdutil.NilUUID {
		return "", fmt.Errorf("template %q has no successful build", input)
	}

	return match.BuildID, nil
}
