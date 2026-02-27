package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/klauspost/compress/zstd"
	lz4 "github.com/pierrec/lz4/v4"

	"github.com/e2b-dev/infra/packages/orchestrator/cmd/internal/cmdutil"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

// bufferPartUploader implements storage.PartUploader for in-memory writes.
// Parts are collected by index and assembled in order on Complete, since
// CompressStream uploads parts concurrently and they may arrive out of order.
type bufferPartUploader struct {
	mu    sync.Mutex
	parts map[int][]byte
	buf   bytes.Buffer
}

func (b *bufferPartUploader) Start(_ context.Context) error {
	b.parts = make(map[int][]byte)

	return nil
}

func (b *bufferPartUploader) UploadPart(_ context.Context, partIndex int, data ...[]byte) error {
	var combined bytes.Buffer
	for _, d := range data {
		combined.Write(d)
	}
	b.mu.Lock()
	b.parts[partIndex] = combined.Bytes()
	b.mu.Unlock()

	return nil
}

func (b *bufferPartUploader) Complete(_ context.Context) error {
	// Assemble parts in order
	keys := make([]int, 0, len(b.parts))
	for k := range b.parts {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		b.buf.Write(b.parts[k])
	}
	b.parts = nil

	return nil
}

type benchResult struct {
	codec      string
	level      int
	rawEncTime time.Duration
	frmEncTime time.Duration
	rawDecTime time.Duration
	frmDecTime time.Duration
	rawSize    int64
	frmSize    int64
	origSize   int64
	numFrames  int
}

func main() {
	build := flag.String("build", "", "build ID")
	template := flag.String("template", "", "template ID or alias (requires E2B_API_KEY)")
	storagePath := flag.String("storage", ".local-build", "storage: local path or gs://bucket")
	doMemfile := flag.Bool("memfile", false, "benchmark memfile only")
	doRootfs := flag.Bool("rootfs", false, "benchmark rootfs only")
	iterations := flag.Int("iterations", 1, "number of iterations for timing (results averaged)")

	flag.Parse()

	cmdutil.SuppressNoisyLogsKeepStdLog()

	// Resolve build ID
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
		fmt.Fprintf(os.Stderr, "Usage: benchmark-compress (-build <uuid> | -template <id-or-alias>) [flags]\n\n")
		fmt.Fprintf(os.Stderr, "Benchmarks raw vs framed compression to measure framing overhead.\n\n")
		flag.PrintDefaults()
		os.Exit(1)
	}

	// Determine which artifacts to benchmark
	type artifact struct {
		name string
		file string
	}
	var artifacts []artifact
	if !*doMemfile && !*doRootfs {
		// Default: both
		artifacts = []artifact{
			{"memfile", storage.MemfileName},
			{"rootfs", storage.RootfsName},
		}
	} else {
		if *doMemfile {
			artifacts = append(artifacts, artifact{"memfile", storage.MemfileName})
		}
		if *doRootfs {
			artifacts = append(artifacts, artifact{"rootfs", storage.RootfsName})
		}
	}

	ctx := context.Background()

	for _, a := range artifacts {
		data, err := loadArtifact(ctx, *storagePath, *build, a.file)
		if err != nil {
			log.Fatalf("failed to load %s: %s", a.name, err)
		}

		printHeader(a.name, int64(len(data)))
		benchmarkArtifact(data, *iterations, func(r benchResult) {
			printRow(r)
		})
		fmt.Println()
	}
}

func loadArtifact(ctx context.Context, storagePath, buildID, file string) ([]byte, error) {
	reader, dataSize, source, err := cmdutil.OpenDataFile(ctx, storagePath, buildID, file)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", file, err)
	}
	defer reader.Close()

	fmt.Printf("Loading %s from %s (%d bytes, %.1f MiB)...\n",
		file, source, dataSize, float64(dataSize)/1024/1024)

	data := make([]byte, dataSize)
	_, err = io.ReadFull(io.NewSectionReader(reader, 0, dataSize), data)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", file, err)
	}

	return data, nil
}

func benchmarkArtifact(data []byte, iterations int, emit func(benchResult)) {
	type codecConfig struct {
		name   string
		ct     storage.CompressionType
		levels []int
	}
	codecs := []codecConfig{
		{"lz4", storage.CompressionLZ4, []int{0, 1}},
		{"zstd", storage.CompressionZstd, []int{
			int(zstd.SpeedFastest),           // 1
			int(zstd.SpeedDefault),           // 2
			int(zstd.SpeedBetterCompression), // 3
			int(zstd.SpeedBestCompression),   // 4
		}},
	}

	for _, codec := range codecs {
		for _, level := range codec.levels {
			r := benchResult{
				codec:    codec.name,
				level:    level,
				origSize: int64(len(data)),
			}

			var rawCompressed, framedCompressed []byte
			var ft *storage.FrameTable

			for range iterations {
				rc, rawDur := rawEncode(data, codec.ct, level)
				fc, fft, frmDur := framedEncode(data, codec.ct, level)

				r.rawEncTime += rawDur
				r.frmEncTime += frmDur

				rawCompressed = rc
				framedCompressed = fc
				ft = fft
			}

			r.rawEncTime /= time.Duration(iterations)
			r.frmEncTime /= time.Duration(iterations)
			r.rawSize = int64(len(rawCompressed))
			r.frmSize = int64(len(framedCompressed))

			if ft != nil {
				r.numFrames = len(ft.Frames)
			}

			// Pre-allocate a shared output buffer for decode benchmarks
			// so both paths pay the same allocation cost (zero).
			decBuf := make([]byte, len(data))

			for range iterations {
				rawDecDur := rawDecode(rawCompressed, codec.ct, decBuf)
				frmDecDur := framedDecode(framedCompressed, ft, codec.ct, decBuf)

				r.rawDecTime += rawDecDur
				r.frmDecTime += frmDecDur
			}

			r.rawDecTime /= time.Duration(iterations)
			r.frmDecTime /= time.Duration(iterations)

			emit(r)
		}
	}
}

func rawEncode(data []byte, ct storage.CompressionType, level int) ([]byte, time.Duration) {
	var buf bytes.Buffer
	buf.Grow(len(data))

	start := time.Now()

	switch ct {
	case storage.CompressionLZ4:
		w := lz4.NewWriter(&buf)
		opts := []lz4.Option{lz4.ConcurrencyOption(1)}
		if level > 0 {
			opts = append(opts, lz4.CompressionLevelOption(lz4.CompressionLevel(1<<(8+level))))
		}
		_ = w.Apply(opts...)
		_, _ = w.Write(data)
		_ = w.Close()

	case storage.CompressionZstd:
		// Match the framed encoder: CompressStream passes TargetFrameSize as
		// windowSize to newZstdEncoder, so we must use the same window here
		// for an apples-to-apples comparison.
		w, err := zstd.NewWriter(&buf,
			zstd.WithEncoderLevel(zstd.EncoderLevel(level)),
			zstd.WithEncoderConcurrency(1),
			zstd.WithWindowSize(2*1024*1024))
		if err != nil {
			log.Fatalf("zstd raw encoder (level %d): %s", level, err)
		}
		_, _ = w.Write(data)
		_ = w.Close()
	}

	elapsed := time.Since(start)

	return buf.Bytes(), elapsed
}

func framedEncode(data []byte, ct storage.CompressionType, level int) ([]byte, *storage.FrameTable, time.Duration) {
	uploader := &bufferPartUploader{}

	opts := &storage.FramedUploadOptions{
		CompressionType:          ct,
		Level:                    level,
		CompressionConcurrency:   1,
		TargetFrameSize:          2 * 1024 * 1024, // 2 MiB
		MaxUncompressedFrameSize: storage.DefaultMaxFrameUncompressedSize,
		TargetPartSize:           50 * 1024 * 1024,
	}

	ctx := context.Background()
	reader := bytes.NewReader(data)

	start := time.Now()
	ft, err := storage.CompressStream(ctx, reader, opts, uploader)
	elapsed := time.Since(start)

	if err != nil {
		log.Fatalf("framed encode failed: %s", err)
	}

	return uploader.buf.Bytes(), ft, elapsed
}

func rawDecode(compressed []byte, ct storage.CompressionType, buf []byte) time.Duration {
	start := time.Now()

	switch ct {
	case storage.CompressionLZ4:
		r := lz4.NewReader(bytes.NewReader(compressed))
		_, _ = io.ReadFull(r, buf)

	case storage.CompressionZstd:
		r, _ := zstd.NewReader(bytes.NewReader(compressed), zstd.WithDecoderConcurrency(1))
		_, _ = io.ReadFull(r, buf)
		r.Close()
	}

	return time.Since(start)
}

func framedDecode(compressed []byte, ft *storage.FrameTable, ct storage.CompressionType, buf []byte) time.Duration {
	if ft == nil || len(ft.Frames) == 0 {
		return 0
	}

	start := time.Now()

	var cOffset int64
	var uOffset int
	for _, frame := range ft.Frames {
		frameData := compressed[cOffset : cOffset+int64(frame.C)]
		frameBuf := buf[uOffset : uOffset+int(frame.U)]
		decompressFrameInto(ct, frameData, frameBuf)
		cOffset += int64(frame.C)
		uOffset += int(frame.U)
	}

	return time.Since(start)
}

// decompressFrameInto decompresses into a pre-allocated buffer to avoid
// per-frame allocation. Uses single-threaded decoders to match rawDecode.
func decompressFrameInto(ct storage.CompressionType, compressed, buf []byte) {
	switch ct {
	case storage.CompressionLZ4:
		r := lz4.NewReader(bytes.NewReader(compressed))
		_, err := io.ReadFull(r, buf)
		if err != nil {
			log.Fatalf("framed lz4 decode failed: %s", err)
		}

	case storage.CompressionZstd:
		r, err := zstd.NewReader(bytes.NewReader(compressed), zstd.WithDecoderConcurrency(1))
		if err != nil {
			log.Fatalf("framed zstd decoder create failed: %s", err)
		}
		_, err = io.ReadFull(r, buf)
		if err != nil {
			log.Fatalf("framed zstd decode failed: %s", err)
		}
		r.Close()
	}
}

// ANSI colors.
const (
	colorReset  = "\033[0m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorRed    = "\033[91m"
)

func overheadColor(pct float64) string {
	switch {
	case pct < 5:
		return colorGreen
	case pct < 15:
		return colorYellow
	default:
		return colorRed
	}
}

// pad right-pads s with spaces to exactly width visible characters.
func pad(s string, width int) string {
	if len(s) >= width {
		return s
	}

	return s + strings.Repeat(" ", width-len(s))
}

// rpad right-aligns s within width visible characters.
func rpad(s string, width int) string {
	if len(s) >= width {
		return s
	}

	return strings.Repeat(" ", width-len(s)) + s
}

// colorWrap wraps text with ANSI color, pre-padded to width so alignment is correct.
func colorWrap(color, text string, width int) string {
	padded := pad(text, width)

	return color + padded + colorReset
}

func fmtSpeed(dataSize int64, d time.Duration) string {
	if d == 0 {
		return rpad("N/A", 9)
	}
	mbps := float64(dataSize) / d.Seconds() / (1024 * 1024)

	return rpad(fmt.Sprintf("%.0f MB/s", mbps), 9)
}

func fmtOverhead(raw, framed time.Duration) string {
	if raw == 0 {
		return pad("N/A", 7)
	}
	pct := float64(framed-raw) / float64(raw) * 100
	text := fmt.Sprintf("%+.1f%%", pct)

	return colorWrap(overheadColor(pct), text, 7)
}

func fmtSizeOH(rawSize, frmSize int64) string {
	if rawSize == 0 {
		return pad("N/A", 7)
	}
	pct := float64(frmSize-rawSize) / float64(rawSize) * 100
	text := fmt.Sprintf("%+.1f%%", pct)

	return colorWrap(overheadColor(pct), text, 7)
}

func fmtMiB(b int64) string {
	return rpad(fmt.Sprintf("%.1f MiB", float64(b)/1024/1024), 9)
}

func printHeader(artifact string, origSize int64) {
	fmt.Printf("\n=== %s (%.1f MiB) ===\n\n", artifact, float64(origSize)/1024/1024)

	hdr := fmt.Sprintf("%-4s  %3s  %9s  %9s  %-7s  %9s  %9s  %-7s  %9s  %9s  %-7s  %-5s  %6s  %8s",
		"Codec", "Lvl",
		"Raw Enc", "Frm Enc", "Enc OH",
		"Raw Dec", "Frm Dec", "Dec OH",
		"Raw Size", "Frm Size", "Size OH",
		"Ratio", "Frames", "Dec/Frm")
	sep := fmt.Sprintf("%-4s  %3s  %9s  %9s  %-7s  %9s  %9s  %-7s  %9s  %9s  %-7s  %-5s  %6s  %8s",
		"----", "---",
		"---------", "---------", "-------",
		"---------", "---------", "-------",
		"---------", "---------", "-------",
		"-----", "------", "--------")
	fmt.Println(hdr)
	fmt.Println(sep)
}

func printRow(r benchResult) {
	ratio := float64(r.origSize) / float64(r.frmSize)
	ratioColor := cmdutil.RatioColor(ratio)
	ratioText := fmt.Sprintf("%.1fx", ratio)
	if ratio >= 100 {
		ratioText = fmt.Sprintf("%.0fx", ratio)
	}

	var decPerFrame string
	if r.numFrames > 0 {
		usPerFrame := r.frmDecTime.Microseconds() / int64(r.numFrames)
		decPerFrame = rpad(fmt.Sprintf("%d us", usPerFrame), 8)
	} else {
		decPerFrame = rpad("N/A", 8)
	}

	fmt.Printf("%-4s  %3d  %s  %s  %s  %s  %s  %s  %s  %s  %s  %s  %6d  %s\n",
		r.codec,
		r.level,
		fmtSpeed(r.origSize, r.rawEncTime),
		fmtSpeed(r.origSize, r.frmEncTime),
		fmtOverhead(r.rawEncTime, r.frmEncTime),
		fmtSpeed(r.origSize, r.rawDecTime),
		fmtSpeed(r.origSize, r.frmDecTime),
		fmtOverhead(r.rawDecTime, r.frmDecTime),
		fmtMiB(r.rawSize),
		fmtMiB(r.frmSize),
		fmtSizeOH(r.rawSize, r.frmSize),
		colorWrap(ratioColor, ratioText, 5),
		r.numFrames,
		decPerFrame,
	)
}

// --- Template resolution (copied from compress-build) ---

type templateInfo struct {
	TemplateID string   `json:"templateID"`
	BuildID    string   `json:"buildID"`
	Aliases    []string `json:"aliases"`
	Names      []string `json:"names"`
}

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
