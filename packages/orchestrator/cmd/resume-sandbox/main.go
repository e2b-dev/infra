// resume-sandbox resumes a sandbox from a built template.
// Example: sudo go run ./cmd/resume-sandbox -local -build <uuid>
// Benchmark: sudo go run ./cmd/resume-sandbox -local -build <uuid> -benchmark 10
// Trace: sudo go run ./cmd/resume-sandbox -local -build <uuid> -trace
package main

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"flag"
	"fmt"
	htmltemplate "html/template"
	"log"
	"math"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"slices"
	"sort"
	"strings"
	"time"

	googleprof "github.com/google/pprof/profile"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/metric/noop"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/cfg"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	blockmetrics "github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block/metrics"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/fc"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/nbd"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/network"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/template"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/trace"
	featureflags "github.com/e2b-dev/infra/packages/shared/pkg/feature-flags"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	sbxlogger "github.com/e2b-dev/infra/packages/shared/pkg/logger/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

func main() {
	buildID := flag.String("build", "", "build ID (UUID, required)")
	kernel := flag.String("kernel", "vmlinux-6.1.102", "kernel version")
	fcVer := flag.String("firecracker", "v1.12.1_717921c", "firecracker version")
	local := flag.Bool("local", false, "use local paths for binaries/working dirs")
	localTemplates := flag.Bool("local-templates", false, "use local template storage (overrides -bucket and TEMPLATE_BUCKET_NAME)")
	bucket := flag.String("bucket", os.Getenv("TEMPLATE_BUCKET_NAME"), "GCS bucket for templates (use with -local for remote templates + local binaries, or set TEMPLATE_BUCKET_NAME env var)")
	dataDir := flag.String("data-dir", ".local-build", "data directory for local mode")
	vcpu := flag.Int64("vcpu", 2, "vCPUs")
	memory := flag.Int64("memory", 512, "memory MB")
	disk := flag.Int64("disk", 2048, "disk MB")
	benchmark := flag.Int("benchmark", 0, "run N benchmark iterations (0 = interactive mode)")
	traceFlag := flag.Bool("trace", false, "enable page fault tracing and output trace data")
	pprofFlag := flag.Bool("pprof", false, "enable CPU profiling during benchmark")
	pprofPort := flag.Int("pprof-port", 6060, "pprof HTTP server port")
	flag.Parse()

	if *buildID == "" {
		log.Fatal("-build required")
	}
	if os.Geteuid() != 0 {
		log.Fatal("run as root")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	go func() { <-sig; fmt.Println("\n🛑 Stopping..."); cancel() }()

	if *local {
		bucketName := *bucket
		if *localTemplates {
			bucketName = "" // Force local templates
		}
		if err := setupLocal(*dataDir, bucketName); err != nil {
			log.Fatal(err)
		}
	}

	if err := run(ctx, *buildID, *kernel, *fcVer, *vcpu, *memory, *disk, *benchmark, *traceFlag, *pprofFlag, *pprofPort); err != nil && ctx.Err() == nil {
		log.Fatal(err)
	}
}

func setupLocal(dataDir string, bucket string) error {
	abs := func(s string) string { return utils.Must(filepath.Abs(s)) }
	for _, d := range []string{"kernels", "templates", "sandbox", "orchestrator", "snapshot-cache", "fc-versions", "envd"} {
		if err := os.MkdirAll(filepath.Join(dataDir, d), 0o755); err != nil {
			return err
		}
	}
	for _, d := range []string{"build", "build-templates", "sandbox", "snapshot-cache", "template"} {
		if err := os.MkdirAll(filepath.Join(dataDir, "orchestrator", d), 0o755); err != nil {
			return err
		}
	}

	// Base local config for binaries and working directories
	envVars := map[string]string{
		"ARTIFACTS_REGISTRY_PROVIDER": "Local",
		"FIRECRACKER_VERSIONS_DIR":    abs(filepath.Join(dataDir, "fc-versions")),
		"HOST_ENVD_PATH":              abs(filepath.Join(dataDir, "envd", "envd")),
		"HOST_KERNELS_DIR":            abs(filepath.Join(dataDir, "kernels")),
		"ORCHESTRATOR_BASE_PATH":      abs(filepath.Join(dataDir, "orchestrator")),
		"SANDBOX_DIR":                 abs(filepath.Join(dataDir, "sandbox")),
		"SNAPSHOT_CACHE_DIR":          abs(filepath.Join(dataDir, "snapshot-cache")),
		"USE_LOCAL_NAMESPACE_STORAGE": "true",
	}

	if bucket != "" {
		// Use remote GCS bucket for templates
		envVars["STORAGE_PROVIDER"] = "GCPBucket"
		envVars["TEMPLATE_BUCKET_NAME"] = bucket
	} else {
		// Use local filesystem for templates
		envVars["STORAGE_PROVIDER"] = "Local"
		envVars["LOCAL_TEMPLATE_STORAGE_BASE_PATH"] = abs(filepath.Join(dataDir, "templates"))
	}

	for k, v := range envVars {
		os.Setenv(k, v)
	}

	return nil
}

type runner struct {
	ctx           context.Context
	factory       *sandbox.Factory
	tmpl          template.Template
	templateCache *template.Cache
	sbxConfig     sandbox.Config
	buildID       string
	trace         bool
	spec          SpecInfo
	pprofEnabled  bool
	pprofPort     int
}

type resumeResult struct {
	StartTime   time.Time
	Duration    time.Duration
	PageFaults  []trace.Event
	NBDEvents   []trace.Event
	PhaseEvents []trace.PhaseEvent
}

// ChunkStats holds statistics about build layers accessed from remote storage.
type ChunkStats struct {
	MemfileLayers int64 // Distinct build layers for memfile
	MemfileChunks int64 // Total chunks fetched for memfile
	RootfsLayers  int64 // Distinct build layers for rootfs
	RootfsChunks  int64 // Total chunks fetched for rootfs
}

func (r *runner) resumeOnce(iter int) (resumeResult, error) {
	runtime := sandbox.RuntimeMetadata{
		TemplateID: r.buildID, TeamID: "local",
		SandboxID:   fmt.Sprintf("sbx-%d-%d", time.Now().UnixNano(), iter),
		ExecutionID: fmt.Sprintf("exec-%d-%d", time.Now().UnixNano(), iter),
	}

	t0 := time.Now()
	sbx, err := r.factory.ResumeSandbox(r.ctx, r.tmpl, r.sbxConfig, runtime, t0, t0.Add(24*time.Hour), nil)
	dur := time.Since(t0)

	var pageFaults []trace.Event
	var nbdEvents []trace.Event
	var phaseEvents []trace.PhaseEvent

	// Extract traces even if there was an error (sandbox may be partially created)
	if sbx != nil {
		if r.trace {
			pageFaults = sbx.GetPageFaultTrace()
			nbdEvents = sbx.GetNBDTrace()
			phaseEvents = sbx.GetPhaseTrace()
		}
		sbx.Close(context.Background())
	}

	return resumeResult{
		StartTime:   t0,
		Duration:    dur,
		PageFaults:  pageFaults,
		NBDEvents:   nbdEvents,
		PhaseEvents: phaseEvents,
	}, err
}

func (r *runner) runInteractive() error {
	runtime := sandbox.RuntimeMetadata{
		TemplateID: r.buildID, TeamID: "local",
		SandboxID:   fmt.Sprintf("sbx-%d", time.Now().UnixNano()),
		ExecutionID: fmt.Sprintf("exec-%d", time.Now().UnixNano()),
	}

	fmt.Println("🚀 Starting...")
	t0 := time.Now()
	sbx, err := r.factory.ResumeSandbox(r.ctx, r.tmpl, r.sbxConfig, runtime, t0, t0.Add(24*time.Hour), nil)
	dur := time.Since(t0)
	if err != nil {
		return err
	}

	// Enable tracing after sandbox is running (for future page faults)
	if r.trace {
		sbx.SetTraceEnabled(true)
	}

	fmt.Printf("✅ Running (resumed in %s)\n", dur)
	fmt.Printf("   sudo nsenter --net=/var/run/netns/%s ssh -o StrictHostKeyChecking=no root@169.254.0.21\n", sbx.Slot.NamespaceID())
	fmt.Println("Ctrl+C to stop")

	<-r.ctx.Done()
	fmt.Println("🧹 Cleanup...")

	if r.trace {
		pageFaults := sbx.GetPageFaultTrace()
		nbdEvents := sbx.GetNBDTrace()
		phaseEvents := sbx.GetPhaseTrace()
		if len(pageFaults) > 0 || len(nbdEvents) > 0 || len(phaseEvents) > 0 {
			// Get memfile header for nil page analysis
			var memfileHeader *header.Header
			if r.tmpl != nil {
				if memfile, err := r.tmpl.Memfile(r.ctx); err == nil && memfile != nil {
					memfileHeader = memfile.Header()
				}
			}
			chunkStats := getChunkStats(r.templateCache)
			exportMultiRunTrace(r.ctx, []TraceRun{{
				Run:         0,
				StartTs:     t0.UnixNano(),
				Duration:    dur.Nanoseconds(),
				Faults:      pageFaults,
				NBDEvents:   nbdEvents,
				PhaseEvents: phaseEvents,
			}}, r.spec, nil, nil, nil, 0, 0, memfileHeader, chunkStats)
		}
	}

	sbx.Close(context.Background())

	return nil
}

func (r *runner) runBenchmark(count int) error {
	var durations []time.Duration
	var traceRuns []TraceRun
	var benchmarkErr error

	// Start CPU profiling if enabled
	var profileBuf bytes.Buffer
	profilingActive := false
	if r.pprofEnabled {
		if err := pprof.StartCPUProfile(&profileBuf); err != nil {
			fmt.Printf("Warning: could not start CPU profile: %v\n", err)
		} else {
			profilingActive = true
		}
	}

	for i := range count {
		if r.ctx.Err() != nil {
			break
		}
		fmt.Printf("[%d/%d] Starting...\n", i+1, count)
		result, err := r.resumeOnce(i)
		durations = append(durations, result.Duration)
		if r.trace {
			traceRuns = append(traceRuns, TraceRun{
				Run:         i,
				StartTs:     result.StartTime.UnixNano(),
				Duration:    result.Duration.Nanoseconds(),
				Faults:      result.PageFaults,
				NBDEvents:   result.NBDEvents,
				PhaseEvents: result.PhaseEvents,
			})
		}
		if err != nil {
			fmt.Printf("[%d/%d] ❌ Failed after %s: %v\n", i+1, count, result.Duration, err)
			benchmarkErr = err
			break // Stop benchmark but continue to export traces
		}
		fmt.Printf("[%d/%d] Resumed in %s", i+1, count, result.Duration)
		if r.trace {
			fmt.Printf(" (%d faults)", len(result.PageFaults))
		}
		fmt.Println()
	}

	// Stop profiling and parse results
	var profileHotspots []ProfileFunction
	var profileCallStacks []ProfileCallStack
	var memoryHotspots []MemoryHotspot
	var totalAllocBytes, totalAllocObjects int64
	if profilingActive {
		pprof.StopCPUProfile()
		if profileBuf.Len() > 0 {
			profileHotspots, profileCallStacks = parseProfile(profileBuf.Bytes(), count)
			fmt.Printf("CPU profile captured: %d bytes, %d hotspots, %d stacks\n", profileBuf.Len(), len(profileHotspots), len(profileCallStacks))
		} else {
			fmt.Println("Warning: CPU profile buffer is empty")
		}

		// Capture heap profile
		var heapBuf bytes.Buffer
		if err := pprof.WriteHeapProfile(&heapBuf); err != nil {
			fmt.Printf("Warning: could not capture heap profile: %v\n", err)
		} else if heapBuf.Len() > 0 {
			memoryHotspots, totalAllocBytes, totalAllocObjects = parseHeapProfile(heapBuf.Bytes(), count)
			fmt.Printf("Heap profile captured: %d bytes, %d hotspots\n", heapBuf.Len(), len(memoryHotspots))
		}
	}

	printStats(durations)

	// Print chunk fetch statistics
	chunkStats := getChunkStats(r.templateCache)
	printChunkStats(chunkStats)

	// Get memfile header for nil page analysis
	var memfileHeader *header.Header
	if r.tmpl != nil {
		memfile, err := r.tmpl.Memfile(r.ctx)
		if err == nil && memfile != nil {
			memfileHeader = memfile.Header()
		}
	}

	if len(traceRuns) > 0 || len(profileHotspots) > 0 || len(memoryHotspots) > 0 {
		exportMultiRunTrace(r.ctx, traceRuns, r.spec, profileHotspots, profileCallStacks, memoryHotspots, totalAllocBytes, totalAllocObjects, memfileHeader, chunkStats)
	}

	return benchmarkErr
}

func printStats(durations []time.Duration) {
	if len(durations) == 0 {
		return
	}

	sorted := make([]time.Duration, len(durations))
	copy(sorted, durations)
	slices.Sort(sorted)

	var total time.Duration
	for _, d := range sorted {
		total += d
	}

	n := len(sorted)
	fmt.Printf("\n📊 Results (%d runs):\n", n)
	fmt.Printf("   Min:  %s\n", sorted[0])
	fmt.Printf("   Max:  %s\n", sorted[n-1])
	fmt.Printf("   Avg:  %s\n", total/time.Duration(n))
	fmt.Printf("   P95:  %s\n", percentile(sorted, 0.95))
	fmt.Printf("   P99:  %s\n", percentile(sorted, 0.99))
}

// getChunkStats retrieves build layer statistics from the template cache.
func getChunkStats(cache *template.Cache) ChunkStats {
	if cache == nil {
		return ChunkStats{}
	}
	memfile, rootfs := cache.LayerStats()
	return ChunkStats{
		MemfileLayers: memfile.UniqueLayerCount,
		MemfileChunks: memfile.TotalChunks,
		RootfsLayers:  rootfs.UniqueLayerCount,
		RootfsChunks:  rootfs.TotalChunks,
	}
}

func printChunkStats(stats ChunkStats) {
	totalLayers := stats.MemfileLayers + stats.RootfsLayers
	totalChunks := stats.MemfileChunks + stats.RootfsChunks
	fmt.Printf("\n📦 Build layers accessed:\n")
	fmt.Printf("   Memfile: %d layers, %d chunks (%d MB)\n", stats.MemfileLayers, stats.MemfileChunks, stats.MemfileChunks*2)
	fmt.Printf("   Rootfs:  %d layers, %d chunks (%d MB)\n", stats.RootfsLayers, stats.RootfsChunks, stats.RootfsChunks*2)
	fmt.Printf("   Total:   %d layers, %d chunks (%d MB)\n", totalLayers, totalChunks, totalChunks*2)
}

func percentile(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(float64(len(sorted)-1) * p)

	return sorted[idx]
}

// TraceRun represents a single sandbox run with its trace data.
type TraceRun struct {
	Run         int
	StartTs     int64
	Duration    int64
	Faults      []trace.Event
	NBDEvents   []trace.Event
	PhaseEvents []trace.PhaseEvent
}

func exportMultiRunTrace(ctx context.Context, runs []TraceRun, spec SpecInfo, profileHotspots []ProfileFunction, profileCallStacks []ProfileCallStack, memoryHotspots []MemoryHotspot, totalAllocBytes, totalAllocObjects int64, memfileHeader *header.Header, chunkStats ChunkStats) {
	if len(runs) == 0 && len(profileHotspots) == 0 && len(memoryHotspots) == 0 {
		fmt.Println("\nNo traces recorded")

		return
	}

	// Print summary
	fmt.Printf("\nTrace Summary (%d runs):\n", len(runs))
	var totalFaults int
	for _, r := range runs {
		totalFaults += len(r.Faults)
		fmt.Printf("   Run %d: %d faults, %s\n", r.Run, len(r.Faults), time.Duration(r.Duration))
	}
	fmt.Printf("   Total faults: %d\n", totalFaults)
	if len(profileHotspots) > 0 {
		fmt.Printf("   CPU profile hotspots: %d functions\n", len(profileHotspots))
	}
	if len(memoryHotspots) > 0 {
		fmt.Printf("   Memory hotspots: %d functions, %s allocated\n", len(memoryHotspots), formatBytes(totalAllocBytes))
	}

	// Generate HTML file
	traceFile := fmt.Sprintf("trace-%d.html", time.Now().Unix())
	if err := generateTraceHTML(ctx, traceFile, runs, spec, profileHotspots, profileCallStacks, memoryHotspots, totalAllocBytes, totalAllocObjects, memfileHeader, chunkStats); err != nil {
		fmt.Printf("Failed to write trace file: %v\n", err)

		return
	}
	fmt.Printf("\n📄 Trace exported to: %s\n", traceFile)
	fmt.Printf("   Open in browser: file://%s/%s\n", utils.Must(os.Getwd()), traceFile)
}

//go:embed trace.html.tmpl
var traceTemplate string

// Template data structures
type SpecInfo struct {
	// VM spec
	TemplateID string
	VCPUs      int64
	MemoryMB   int64
	DiskMB     int64
	Kernel     string
	FCVersion  string
	// Host spec
	HostCPU      string
	HostCores    int
	HostMemoryGB int
	HostOS       string
	Hostname     string
}

type ProfileFunction struct {
	Name    string
	Flat    string   // Total time in this function
	FlatAvg string   // Average time per run
	FlatPct string   // Percentage
	Cum     string   // Cumulative time (including callees)
	CumPct  string   // Cumulative percentage
	Callers []string // Top callers of this function
}

type ProfileCallStack struct {
	Stack    string // Full call stack
	Flat     string // Total time
	FlatAvg  string // Average time per run
	FlatPct  string // Percentage
	AppFrame string // First non-runtime frame
}

// MemoryHotspot represents a function that allocates significant memory
type MemoryHotspot struct {
	Name       string   // Function name
	AllocBytes string   // Total bytes allocated (flat)
	AllocAvg   string   // Average bytes per run
	AllocPct   string   // Percentage of total allocations
	AllocObjs  string   // Number of objects allocated
	Callers    []string // Top callers
}

type TemplateData struct {
	Spec              SpecInfo
	NumRuns           int
	TotalFaults       int
	TotalNBDEvents    int
	MinDuration       string
	AvgDuration       string
	P95Duration       string
	MaxDuration       string
	Runs              []RunData
	PageAnalysis      *PageAnalysisData
	ProfileHotspots   []ProfileFunction
	ProfileCallStacks []ProfileCallStack
	ProfileEnabled    bool
	// Memory profiling
	MemoryHotspots    []MemoryHotspot
	TotalAllocBytes   string // Total bytes allocated during benchmark
	TotalAllocObjects string // Total objects allocated
	HasMemoryProfile  bool
	// Build layer stats
	MemfileLayers int64
	MemfileChunks int64
	RootfsLayers  int64
	RootfsChunks  int64
	TotalLayers   int64
	TotalChunks   int64
	TotalChunksMB int64
}

type PageAnalysisData struct {
	TotalUniquePages  int
	PagesInAllRuns    int
	PagesInAllRunsPct string
	AvgPagesPerRun    int
	TopPages          []PageFrequency   // Top 10 most frequent pages
	FrequencyDist     []FrequencyBucket // Distribution of pages by run count
	OrderConsistency  string            // Percentage of page order consistency
	AvgPositionChange string            // Average position change per page across runs
	// Ordering deviation chart data - each run is a series of points
	OrderingChart    []OrderingRunData
	AvgDeviationLine []AvgDeviationPoint // Average deviation at each position
	// Nil/empty page analysis
	TotalNilPages  int    // Number of unique pages that map to nil/empty (uuid.Nil)
	NilPagesPct    string // Percentage of faulted pages that are nil
	TotalNilFaults int    // Total faults for nil pages (across all runs)
}

// OrderingRunData contains deviation data for one run
type OrderingRunData struct {
	Run    int
	Points []OrderingPoint
}

// OrderingPoint represents one page fault's deviation from expected position
type OrderingPoint struct {
	X         int     // Fault order (1, 2, 3, ...)
	Y         float64 // Deviation from expected position (positive = later, negative = earlier)
	Page      int64   // Page number
	ExpectedX float64 // Expected position based on average
}

// AvgDeviationPoint represents the average deviation at a given position
type AvgDeviationPoint struct {
	X      int     // Position (1, 2, 3, ...)
	AvgY   float64 // Average deviation across all runs at this position
	StdDev float64 // Standard deviation (for error bars)
	Count  int     // Number of data points at this position
}

type PageFrequency struct {
	Page        int64
	Count       int
	Pct         string
	AvgOrder    string // Average position in request order
	MinOrder    int    // Earliest position seen
	MaxOrder    int    // Latest position seen
	OrderSpread int    // MaxOrder - MinOrder
}

type FrequencyBucket struct {
	RunCount   int    // Number of runs the page appeared in
	PageCount  int    // How many pages have this frequency
	Pct        string // Percentage of total unique pages
	Cumulative string // Cumulative percentage from this bucket down
	AvgOrder   string // Average order position for pages in this bucket
	AvgSpread  string // Average spread (max-min position) for pages in this bucket
	ZeroSpread int    // Count of pages with 0 spread (deterministic order)
}

type RunData struct {
	RunNum             int
	NumFaults          int
	NumNBDEvents       int
	NumPhaseEvents     int
	PhaseTotalDuration string
	Duration           string
	ServingTime    string
	NBDServingTime string
	NumGaps        int
	MinServe       string
	AvgServe       string
	P99Serve       string
	MaxServe       string
	MinNBD         string
	AvgNBD         string
	P99NBD         string
	MaxNBD         string
	TimelineHeight int
	ReadyPct       string
	Axis1          string
	Axis2          string
	Axis3          string
	Axis4          string
	MaxLanes       int
	MaxNBDLanes    int
	Gaps           []GapData
	Faults         []FaultData
	NBDEvents      []NBDEventData
	PhaseEvents    []PhaseEventData
}

type PhaseEventData struct {
	Name        string
	Duration    string
	StartOffset string
}

type GapData struct {
	LeftPct  string
	WidthPct string
	Duration string
}

type FaultData struct {
	LeftPct  string
	WidthPct string
	Top      int
	Height   int
	Num      int
	Duration string
	Page     int64
}

type NBDEventData struct {
	LeftPct  string
	WidthPct string
	Top      int
	Height   int
	Num      int
	Duration string
	Offset   int64
	Length   int64
	IsWrite  bool
}

func generateTraceHTML(ctx context.Context, filename string, runs []TraceRun, spec SpecInfo, profileHotspots []ProfileFunction, profileCallStacks []ProfileCallStack, memoryHotspots []MemoryHotspot, totalAllocBytes, totalAllocObjects int64, memfileHeader *header.Header, chunkStats ChunkStats) error {
	tmpl, err := htmltemplate.New("trace").Parse(traceTemplate)
	if err != nil {
		return fmt.Errorf("parse template: %w", err)
	}

	f, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer f.Close()

	data := buildTemplateData(ctx, runs, spec, profileHotspots, profileCallStacks, memoryHotspots, totalAllocBytes, totalAllocObjects, memfileHeader, chunkStats)

	return tmpl.Execute(f, data)
}

func buildTemplateData(ctx context.Context, runs []TraceRun, spec SpecInfo, profileHotspots []ProfileFunction, profileCallStacks []ProfileCallStack, memoryHotspots []MemoryHotspot, totalAllocBytes, totalAllocObjects int64, memfileHeader *header.Header, chunkStats ChunkStats) TemplateData {
	var globalMaxDuration int64
	var totalFaults int
	var totalNBDEvents int
	var totalDuration int64

	for _, run := range runs {
		if run.Duration > globalMaxDuration {
			globalMaxDuration = run.Duration
		}
		totalFaults += len(run.Faults)
		totalNBDEvents += len(run.NBDEvents)
		totalDuration += run.Duration
	}

	var minDuration, avgDuration, p95Duration int64
	if len(runs) > 0 {
		avgDuration = totalDuration / int64(len(runs))

		// Calculate min and p95
		durations := make([]int64, len(runs))
		for i, run := range runs {
			durations[i] = run.Duration
		}
		slices.Sort(durations)
		minDuration = durations[0]
		p95Duration = durations[int(float64(len(durations)-1)*0.95)]
	}

	var runDataList []RunData
	for _, run := range runs {
		if rd := buildRunData(run, globalMaxDuration); rd != nil {
			runDataList = append(runDataList, *rd)
		}
	}

	// Analyze page faults across runs
	pageAnalysis := analyzePageFaults(ctx, runs, memfileHeader)

	totalLayers := chunkStats.MemfileLayers + chunkStats.RootfsLayers
	totalChunks := chunkStats.MemfileChunks + chunkStats.RootfsChunks
	return TemplateData{
		Spec:              spec,
		NumRuns:           len(runs),
		TotalFaults:       totalFaults,
		TotalNBDEvents:    totalNBDEvents,
		MinDuration:       formatDuration(minDuration),
		AvgDuration:       formatDuration(avgDuration),
		P95Duration:       formatDuration(p95Duration),
		MaxDuration:       formatDuration(globalMaxDuration),
		Runs:              runDataList,
		PageAnalysis:      pageAnalysis,
		ProfileHotspots:   profileHotspots,
		ProfileCallStacks: profileCallStacks,
		ProfileEnabled:    len(profileHotspots) > 0,
		MemoryHotspots:    memoryHotspots,
		TotalAllocBytes:   formatBytes(totalAllocBytes),
		TotalAllocObjects: fmt.Sprintf("%d", totalAllocObjects),
		HasMemoryProfile:  len(memoryHotspots) > 0,
		MemfileLayers:     chunkStats.MemfileLayers,
		MemfileChunks:     chunkStats.MemfileChunks,
		RootfsLayers:      chunkStats.RootfsLayers,
		RootfsChunks:      chunkStats.RootfsChunks,
		TotalLayers:       totalLayers,
		TotalChunks:       totalChunks,
		TotalChunksMB:     totalChunks * 4, // Each chunk is 4MB
	}
}

const pageSize = 2 * 1024 * 1024 // 2 MiB huge pages

func analyzePageFaults(ctx context.Context, runs []TraceRun, memfileHeader *header.Header) *PageAnalysisData {
	if len(runs) < 2 {
		return nil
	}

	// Collect page data per run
	// pageToRuns maps page number to list of (runIndex, orderInRun)
	type pageOccurrence struct {
		run   int
		order int
	}
	pageOccurrences := make(map[int64][]pageOccurrence)
	var totalPagesAcrossRuns int

	for runIdx, run := range runs {
		seen := make(map[int64]bool)
		for order, fault := range run.Faults {
			page := fault.Offset / int64(pageSize)
			if !seen[page] {
				seen[page] = true
				pageOccurrences[page] = append(pageOccurrences[page], pageOccurrence{run: runIdx, order: order})
			}
		}
		totalPagesAcrossRuns += len(seen)
	}

	totalUniquePages := len(pageOccurrences)
	avgPagesPerRun := totalPagesAcrossRuns / len(runs)

	// Count pages that appear in all runs
	pagesInAllRuns := 0
	for _, occurrences := range pageOccurrences {
		if len(occurrences) == len(runs) {
			pagesInAllRuns++
		}
	}
	pagesInAllRunsPct := float64(pagesInAllRuns) / float64(totalUniquePages) * 100

	// Find top 10 most frequent pages
	type pageCount struct {
		page   int64
		count  int
		orders []int
	}
	var pageCounts []pageCount
	for page, occurrences := range pageOccurrences {
		var orders []int
		for _, o := range occurrences {
			orders = append(orders, o.order)
		}
		pageCounts = append(pageCounts, pageCount{page: page, count: len(occurrences), orders: orders})
	}
	sort.Slice(pageCounts, func(i, j int) bool {
		if pageCounts[i].count != pageCounts[j].count {
			return pageCounts[i].count > pageCounts[j].count
		}

		return pageCounts[i].page < pageCounts[j].page
	})

	// Build frequency distribution (how many pages appear in N runs)
	// Also track average order and spread for pages in each bucket
	type bucketData struct {
		count       int
		totalOrder  float64
		orderCount  int
		totalSpread float64
		zeroSpread  int
	}
	freqDist := make(map[int]*bucketData) // runCount -> bucket data
	for _, pc := range pageCounts {
		if freqDist[pc.count] == nil {
			freqDist[pc.count] = &bucketData{}
		}
		freqDist[pc.count].count++
		for _, order := range pc.orders {
			freqDist[pc.count].totalOrder += float64(order)
			freqDist[pc.count].orderCount++
		}
		// Calculate spread for this page
		if len(pc.orders) > 1 {
			minO, maxO := pc.orders[0], pc.orders[0]
			for _, o := range pc.orders {
				if o < minO {
					minO = o
				}
				if o > maxO {
					maxO = o
				}
			}
			spread := maxO - minO
			freqDist[pc.count].totalSpread += float64(spread)
			if spread == 0 {
				freqDist[pc.count].zeroSpread++
			}
		} else {
			// Single occurrence = 0 spread by definition
			freqDist[pc.count].zeroSpread++
		}
	}
	var freqBuckets []FrequencyBucket
	cumulative := 0
	// Go from lowest to highest frequency
	for runCount := 1; runCount <= len(runs); runCount++ {
		if bd, exists := freqDist[runCount]; exists {
			cumulative += bd.count
			avgOrder := float64(0)
			if bd.orderCount > 0 {
				avgOrder = bd.totalOrder / float64(bd.orderCount)
			}
			avgSpread := float64(0)
			if bd.count > 0 {
				avgSpread = bd.totalSpread / float64(bd.count)
			}
			freqBuckets = append(freqBuckets, FrequencyBucket{
				RunCount:   runCount,
				PageCount:  bd.count,
				Pct:        fmt.Sprintf("%.1f%%", float64(bd.count)/float64(totalUniquePages)*100),
				Cumulative: fmt.Sprintf("%.1f%%", float64(cumulative)/float64(totalUniquePages)*100),
				AvgOrder:   fmt.Sprintf("%.1f", avgOrder),
				AvgSpread:  fmt.Sprintf("%.1f", avgSpread),
				ZeroSpread: bd.zeroSpread,
			})
		}
	}

	// Build top pages list, grouping 100% freq + 0 spread pages to show more variety
	var topPages []PageFrequency
	var deterministicPages []PageFrequency // 100% freq, 0 spread
	var variablePages []PageFrequency      // everything else

	for _, pc := range pageCounts {
		pct := float64(pc.count) / float64(len(runs)) * 100
		var avgOrder float64
		minOrder, maxOrder := pc.orders[0], pc.orders[0]
		for _, o := range pc.orders {
			avgOrder += float64(o)
			if o < minOrder {
				minOrder = o
			}
			if o > maxOrder {
				maxOrder = o
			}
		}
		avgOrder /= float64(len(pc.orders))
		spread := maxOrder - minOrder

		pf := PageFrequency{
			Page:        pc.page,
			Count:       pc.count,
			Pct:         fmt.Sprintf("%.0f%%", pct),
			AvgOrder:    fmt.Sprintf("%.1f", avgOrder),
			MinOrder:    minOrder,
			MaxOrder:    maxOrder,
			OrderSpread: spread,
		}

		if pc.count == len(runs) && spread == 0 {
			deterministicPages = append(deterministicPages, pf)
		} else {
			variablePages = append(variablePages, pf)
		}
	}

	// Sort deterministic pages by order (they have fixed order)
	sort.Slice(deterministicPages, func(i, j int) bool {
		return deterministicPages[i].MinOrder < deterministicPages[j].MinOrder
	})

	// Show first 5 deterministic, then up to 10 variable pages
	maxDeterministic := min(len(deterministicPages), 5)
	for i := 0; i < maxDeterministic; i++ {
		topPages = append(topPages, deterministicPages[i])
	}
	// Add a summary row if there are more deterministic pages
	if len(deterministicPages) > maxDeterministic {
		topPages = append(topPages, PageFrequency{
			Page:        -1, // Marker for summary row
			Count:       len(deterministicPages) - maxDeterministic,
			Pct:         "100%",
			AvgOrder:    "...",
			MinOrder:    -1,
			MaxOrder:    -1,
			OrderSpread: 0,
		})
	}

	// Add variable pages (sorted by frequency desc, then spread desc)
	sort.Slice(variablePages, func(i, j int) bool {
		if variablePages[i].Count != variablePages[j].Count {
			return variablePages[i].Count > variablePages[j].Count
		}

		return variablePages[i].OrderSpread > variablePages[j].OrderSpread
	})
	maxVariable := min(len(variablePages), 10)
	for i := 0; i < maxVariable; i++ {
		topPages = append(topPages, variablePages[i])
	}

	// Calculate order consistency using Kendall tau-like metric
	// Compare order of common pages between consecutive runs
	var totalPairs, consistentPairs int
	for runIdx := 0; runIdx < len(runs)-1; runIdx++ {
		run1 := runs[runIdx]
		run2 := runs[runIdx+1]

		// Build page order maps
		order1 := make(map[int64]int)
		order2 := make(map[int64]int)
		for i, f := range run1.Faults {
			page := f.Offset / int64(pageSize)
			if _, exists := order1[page]; !exists {
				order1[page] = i
			}
		}
		for i, f := range run2.Faults {
			page := f.Offset / int64(pageSize)
			if _, exists := order2[page]; !exists {
				order2[page] = i
			}
		}

		// Find common pages
		var commonPages []int64
		for page := range order1 {
			if _, exists := order2[page]; exists {
				commonPages = append(commonPages, page)
			}
		}

		// Count concordant pairs
		for i := 0; i < len(commonPages); i++ {
			for j := i + 1; j < len(commonPages); j++ {
				p1, p2 := commonPages[i], commonPages[j]
				// Concordant if relative order is the same
				sign1 := order1[p1] < order1[p2]
				sign2 := order2[p1] < order2[p2]
				totalPairs++
				if sign1 == sign2 {
					consistentPairs++
				}
			}
		}
	}

	orderConsistency := float64(0)
	if totalPairs > 0 {
		orderConsistency = float64(consistentPairs) / float64(totalPairs) * 100
	}

	// Calculate average position change for pages across runs
	var totalPositionChange float64
	var pagesWithMultipleOccurrences int
	for _, occurrences := range pageOccurrences {
		if len(occurrences) < 2 {
			continue
		}
		pagesWithMultipleOccurrences++
		var minOrder, maxOrder int
		for i, o := range occurrences {
			if i == 0 || o.order < minOrder {
				minOrder = o.order
			}
			if i == 0 || o.order > maxOrder {
				maxOrder = o.order
			}
		}
		totalPositionChange += float64(maxOrder - minOrder)
	}
	avgPositionChange := float64(0)
	if pagesWithMultipleOccurrences > 0 {
		avgPositionChange = totalPositionChange / float64(pagesWithMultipleOccurrences)
	}

	// Build ordering deviation chart data
	// For each page, calculate its average position across all runs
	pageAvgPosition := make(map[int64]float64)
	for page, occurrences := range pageOccurrences {
		var sum float64
		for _, o := range occurrences {
			sum += float64(o.order)
		}
		pageAvgPosition[page] = sum / float64(len(occurrences))
	}

	// For each run, calculate deviation of each fault from expected position
	var orderingChart []OrderingRunData
	for runIdx, run := range runs {
		if len(run.Faults) == 0 {
			continue
		}

		var points []OrderingPoint
		seen := make(map[int64]bool)

		for order, fault := range run.Faults {
			page := fault.Offset / int64(pageSize)
			if seen[page] {
				continue // Skip duplicate pages within same run
			}
			seen[page] = true

			expectedPos := pageAvgPosition[page]
			deviation := float64(order) - expectedPos

			points = append(points, OrderingPoint{
				X:         order,
				Y:         deviation,
				Page:      page,
				ExpectedX: expectedPos,
			})
		}

		orderingChart = append(orderingChart, OrderingRunData{
			Run:    runIdx,
			Points: points,
		})
	}

	// Calculate average deviation at each position across all runs
	// Collect all deviations by position
	positionDeviations := make(map[int][]float64)
	for _, runData := range orderingChart {
		for _, pt := range runData.Points {
			positionDeviations[pt.X] = append(positionDeviations[pt.X], pt.Y)
		}
	}

	// Calculate average and std dev for each position
	var avgDeviationLine []AvgDeviationPoint
	maxPos := 0
	for pos := range positionDeviations {
		if pos > maxPos {
			maxPos = pos
		}
	}

	for pos := 0; pos <= maxPos; pos++ {
		devs := positionDeviations[pos]
		if len(devs) == 0 {
			continue
		}

		// Calculate mean
		var sum float64
		for _, d := range devs {
			sum += d
		}
		avg := sum / float64(len(devs))

		// Calculate standard deviation
		var sqDiffSum float64
		for _, d := range devs {
			diff := d - avg
			sqDiffSum += diff * diff
		}
		stdDev := 0.0
		if len(devs) > 1 {
			stdDev = math.Sqrt(sqDiffSum / float64(len(devs)-1))
		}

		avgDeviationLine = append(avgDeviationLine, AvgDeviationPoint{
			X:      pos,
			AvgY:   avg,
			StdDev: stdDev,
			Count:  len(devs),
		})
	}

	// Sort by position
	sort.Slice(avgDeviationLine, func(i, j int) bool {
		return avgDeviationLine[i].X < avgDeviationLine[j].X
	})

	// Analyze nil/empty pages using the header
	var totalNilPages int
	var totalNilFaults int
	nilPagesPct := "N/A"

	if memfileHeader != nil {
		nilPages := make(map[int64]bool)
		for page := range pageOccurrences {
			offset := page * int64(pageSize)
			_, _, buildID, err := memfileHeader.GetShiftedMapping(ctx, offset)
			if err == nil && buildID != nil && *buildID == uuid.Nil {
				nilPages[page] = true
			}
		}
		totalNilPages = len(nilPages)

		// Count total faults for nil pages
		for _, run := range runs {
			for _, fault := range run.Faults {
				page := fault.Offset / int64(pageSize)
				if nilPages[page] {
					totalNilFaults++
				}
			}
		}

		if totalUniquePages > 0 {
			nilPagesPct = fmt.Sprintf("%.1f%%", float64(totalNilPages)/float64(totalUniquePages)*100)
		}
	}

	return &PageAnalysisData{
		TotalUniquePages:  totalUniquePages,
		PagesInAllRuns:    pagesInAllRuns,
		PagesInAllRunsPct: fmt.Sprintf("%.1f%%", pagesInAllRunsPct),
		AvgPagesPerRun:    avgPagesPerRun,
		TopPages:          topPages,
		FrequencyDist:     freqBuckets,
		OrderConsistency:  fmt.Sprintf("%.1f%%", orderConsistency),
		AvgPositionChange: fmt.Sprintf("%.1f", avgPositionChange),
		OrderingChart:     orderingChart,
		AvgDeviationLine:  avgDeviationLine,
		TotalNilPages:     totalNilPages,
		NilPagesPct:       nilPagesPct,
		TotalNilFaults:    totalNilFaults,
	}
}

func buildRunData(run TraceRun, globalMaxDuration int64) *RunData {
	faults := run.Faults
	nbdEvents := run.NBDEvents
	if len(faults) == 0 && len(nbdEvents) == 0 {
		return nil
	}

	// Safeguard: ensure globalMaxDuration is not zero
	if globalMaxDuration <= 0 {
		// Use run.Duration as fallback, or 1ms minimum to avoid divide by zero
		globalMaxDuration = run.Duration
		if globalMaxDuration <= 0 {
			globalMaxDuration = 1_000_000 // 1ms minimum
		}
	}

	startTs := run.StartTs

	// Sort faults by timestamp
	sorted := make([]trace.Event, len(faults))
	copy(sorted, faults)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Timestamp < sorted[j].Timestamp })

	if startTs == 0 && len(sorted) > 0 {
		startTs = sorted[0].Timestamp
	}

	// Sort NBD events by timestamp
	sortedNBD := make([]trace.Event, len(nbdEvents))
	copy(sortedNBD, nbdEvents)
	sort.Slice(sortedNBD, func(i, j int) bool { return sortedNBD[i].Timestamp < sortedNBD[j].Timestamp })

	if startTs == 0 && len(sortedNBD) > 0 {
		startTs = sortedNBD[0].Timestamp
	}

	// Assign lanes for concurrent faults
	lanes := []int64{}
	faultLanes := make([]int, len(sorted))
	for i, f := range sorted {
		lane := 0
		for l, endTime := range lanes {
			if endTime <= f.Timestamp {
				lane = l

				break
			}
			lane = l + 1
		}
		if lane >= len(lanes) {
			lanes = append(lanes, 0)
		}
		lanes[lane] = f.Timestamp + f.Duration
		faultLanes[i] = lane
	}
	maxLanes := len(lanes)

	// Assign lanes for concurrent NBD events
	nbdLanes := []int64{}
	nbdEventLanes := make([]int, len(sortedNBD))
	for i, e := range sortedNBD {
		lane := 0
		for l, endTime := range nbdLanes {
			if endTime <= e.Timestamp {
				lane = l

				break
			}
			lane = l + 1
		}
		if lane >= len(nbdLanes) {
			nbdLanes = append(nbdLanes, 0)
		}
		nbdLanes[lane] = e.Timestamp + e.Duration
		nbdEventLanes[i] = lane
	}
	maxNBDLanes := len(nbdLanes)

	// Find gaps > 2ms (using merged intervals - gaps only when NEITHER UFFD nor NBD is serving)
	type gap struct{ start, duration int64 }
	var gaps []gap

	// Combine UFFD and NBD events into a single list of intervals
	type interval struct{ start, end int64 }
	var allIntervals []interval

	// Add UFFD fault intervals
	for _, f := range sorted {
		allIntervals = append(allIntervals, interval{f.Timestamp, f.Timestamp + f.Duration})
	}

	// Add NBD event intervals
	for _, e := range sortedNBD {
		allIntervals = append(allIntervals, interval{e.Timestamp, e.Timestamp + e.Duration})
	}

	if len(allIntervals) > 0 {
		// Sort all intervals by start time
		sort.Slice(allIntervals, func(i, j int) bool {
			return allIntervals[i].start < allIntervals[j].start
		})

		// Merge overlapping intervals
		var merged []interval
		current := allIntervals[0]
		for i := 1; i < len(allIntervals); i++ {
			if allIntervals[i].start <= current.end {
				if allIntervals[i].end > current.end {
					current.end = allIntervals[i].end
				}
			} else {
				merged = append(merged, current)
				current = allIntervals[i]
			}
		}
		merged = append(merged, current)

		// Find gaps between merged intervals (truly idle periods)
		for i := 1; i < len(merged); i++ {
			gapDur := merged[i].start - merged[i-1].end
			if gapDur > 2_000_000 { // 2ms threshold
				gaps = append(gaps, gap{start: merged[i-1].end, duration: gapDur})
			}
		}

		// Check gap from last merged interval to ready
		readyTs := startTs + run.Duration
		lastEnd := merged[len(merged)-1].end
		finalGap := readyTs - lastEnd
		if finalGap > 2_000_000 { // 2ms threshold
			gaps = append(gaps, gap{start: lastEnd, duration: finalGap})
		}
	}

	// Calculate UFFD stats
	var totalServe int64
	var minServe, maxServe, avgServe, p99Serve int64
	if len(sorted) > 0 {
		// Calculate non-overlapping serving time by merging intervals
		totalServe = mergedIntervalDuration(sorted)

		durations := make([]int64, len(sorted))
		var sumDurations int64
		for i, f := range sorted {
			durations[i] = f.Duration
			sumDurations += f.Duration
		}
		slices.Sort(durations)
		minServe = durations[0]
		maxServe = durations[len(durations)-1]
		avgServe = sumDurations / int64(len(sorted))
		p99Serve = durations[int(float64(len(durations)-1)*0.99)]
	}

	// Calculate NBD stats
	var totalNBDServe int64
	var minNBD, maxNBD, avgNBD, p99NBD int64
	if len(sortedNBD) > 0 {
		// Calculate non-overlapping serving time
		totalNBDServe = mergedNBDIntervalDuration(sortedNBD)

		nbdDurations := make([]int64, len(sortedNBD))
		var sumNBDDurations int64
		for i, e := range sortedNBD {
			nbdDurations[i] = e.Duration
			sumNBDDurations += e.Duration
		}
		slices.Sort(nbdDurations)
		minNBD = nbdDurations[0]
		maxNBD = nbdDurations[len(nbdDurations)-1]
		avgNBD = sumNBDDurations / int64(len(sortedNBD))
		p99NBD = nbdDurations[int(float64(len(nbdDurations)-1)*0.99)]
	}

	laneHeight := 18
	laneGap := 2
	// Height: 40px top for events/axis + UFFD lanes + 20px separator + NBD lanes + 20px bottom
	uffdHeight := maxLanes * laneHeight
	nbdHeight := maxNBDLanes * laneHeight
	timelineHeight := 40 + uffdHeight + 20 + nbdHeight + 20

	// Build template gaps
	var gapData []GapData
	for _, g := range gaps {
		leftPct := float64(g.start-startTs) / float64(globalMaxDuration) * 100
		widthPct := float64(g.duration) / float64(globalMaxDuration) * 100
		if widthPct < 0.1 {
			widthPct = 0.1
		}
		gapData = append(gapData, GapData{
			LeftPct:  fmt.Sprintf("%.2f", leftPct),
			WidthPct: fmt.Sprintf("%.2f", widthPct),
			Duration: formatDuration(g.duration),
		})
	}

	// Build template faults (UFFD - top section)
	var faultData []FaultData
	uffdTopOffset := 25
	for i, fault := range sorted {
		leftPct := float64(fault.Timestamp-startTs) / float64(globalMaxDuration) * 100
		widthPct := float64(fault.Duration) / float64(globalMaxDuration) * 100
		if widthPct < 0.05 {
			widthPct = 0.05
		}
		top := uffdTopOffset + faultLanes[i]*laneHeight
		barHeight := laneHeight - laneGap
		pageNum := fault.Offset / (2 * 1024 * 1024)
		faultData = append(faultData, FaultData{
			LeftPct:  fmt.Sprintf("%.2f", leftPct),
			WidthPct: fmt.Sprintf("%.2f", widthPct),
			Top:      top,
			Height:   barHeight,
			Num:      i,
			Duration: formatDuration(fault.Duration),
			Page:     pageNum,
		})
	}

	// Build NBD events (NBD - bottom section)
	var nbdEventData []NBDEventData
	nbdTopOffset := uffdTopOffset + uffdHeight + 20 // Below UFFD section with separator
	for i, evt := range sortedNBD {
		leftPct := float64(evt.Timestamp-startTs) / float64(globalMaxDuration) * 100
		widthPct := float64(evt.Duration) / float64(globalMaxDuration) * 100
		if widthPct < 0.05 {
			widthPct = 0.05
		}
		top := nbdTopOffset + nbdEventLanes[i]*laneHeight
		barHeight := laneHeight - laneGap
		offsetMB := evt.Offset / (1024 * 1024)
		lengthKB := evt.Length / 1024
		nbdEventData = append(nbdEventData, NBDEventData{
			LeftPct:  fmt.Sprintf("%.2f", leftPct),
			WidthPct: fmt.Sprintf("%.2f", widthPct),
			Top:      top,
			Height:   barHeight,
			Num:      i,
			Duration: formatDuration(evt.Duration),
			Offset:   offsetMB,
			Length:   lengthKB,
			IsWrite:  evt.Type == trace.TypeWrite,
		})
	}

	readyPct := float64(run.Duration) / float64(globalMaxDuration) * 100

	// Build phase event data (FC operations list)
	var phaseEventData []PhaseEventData
	var phaseTotalDuration int64
	for _, evt := range run.PhaseEvents {
		startOffset := evt.Timestamp - startTs
		if startOffset < 0 {
			startOffset = 0
		}
		phaseTotalDuration += evt.Duration
		phaseEventData = append(phaseEventData, PhaseEventData{
			Name:        evt.Name,
			Duration:    formatDuration(evt.Duration),
			StartOffset: formatDuration(startOffset),
		})
	}

	return &RunData{
		RunNum:             run.Run,
		NumFaults:          len(faults),
		NumNBDEvents:       len(nbdEvents),
		NumPhaseEvents:     len(run.PhaseEvents),
		PhaseTotalDuration: formatDuration(phaseTotalDuration),
		Duration:           formatDuration(run.Duration),
		ServingTime:    formatDuration(totalServe),
		NBDServingTime: formatDuration(totalNBDServe),
		NumGaps:        len(gaps),
		MinServe:       formatDuration(minServe),
		AvgServe:       formatDuration(avgServe),
		P99Serve:       formatDuration(p99Serve),
		MaxServe:       formatDuration(maxServe),
		MinNBD:         formatDuration(minNBD),
		AvgNBD:         formatDuration(avgNBD),
		P99NBD:         formatDuration(p99NBD),
		MaxNBD:         formatDuration(maxNBD),
		TimelineHeight: timelineHeight,
		ReadyPct:       fmt.Sprintf("%.2f", readyPct),
		Axis1:          formatDuration(globalMaxDuration / 4),
		Axis2:          formatDuration(globalMaxDuration / 2),
		Axis3:          formatDuration(globalMaxDuration * 3 / 4),
		Axis4:          formatDuration(globalMaxDuration),
		MaxLanes:       maxLanes,
		MaxNBDLanes:    maxNBDLanes,
		Gaps:           gapData,
		Faults:         faultData,
		NBDEvents:      nbdEventData,
		PhaseEvents:    phaseEventData,
	}
}

// mergedIntervalDuration calculates the total non-overlapping time spent serving faults.
// It merges overlapping intervals to avoid double-counting parallel operations.
func mergedIntervalDuration(faults []trace.Event) int64 {
	if len(faults) == 0 {
		return 0
	}

	// Create interval pairs (start, end)
	type interval struct{ start, end int64 }
	intervals := make([]interval, len(faults))
	for i, f := range faults {
		intervals[i] = interval{f.Timestamp, f.Timestamp + f.Duration}
	}

	// Sort by start time
	sort.Slice(intervals, func(i, j int) bool {
		return intervals[i].start < intervals[j].start
	})

	// Merge overlapping intervals
	var merged []interval
	current := intervals[0]
	for i := 1; i < len(intervals); i++ {
		if intervals[i].start <= current.end {
			// Overlapping, extend current interval
			if intervals[i].end > current.end {
				current.end = intervals[i].end
			}
		} else {
			// Non-overlapping, save current and start new
			merged = append(merged, current)
			current = intervals[i]
		}
	}
	merged = append(merged, current)

	// Sum the merged intervals
	var total int64
	for _, iv := range merged {
		total += iv.end - iv.start
	}

	return total
}

// mergedNBDIntervalDuration calculates the total non-overlapping time for NBD events.
func mergedNBDIntervalDuration(events []trace.Event) int64 {
	if len(events) == 0 {
		return 0
	}

	type interval struct{ start, end int64 }
	intervals := make([]interval, len(events))
	for i, e := range events {
		intervals[i] = interval{e.Timestamp, e.Timestamp + e.Duration}
	}

	sort.Slice(intervals, func(i, j int) bool {
		return intervals[i].start < intervals[j].start
	})

	var merged []interval
	current := intervals[0]
	for i := 1; i < len(intervals); i++ {
		if intervals[i].start <= current.end {
			if intervals[i].end > current.end {
				current.end = intervals[i].end
			}
		} else {
			merged = append(merged, current)
			current = intervals[i]
		}
	}
	merged = append(merged, current)

	var total int64
	for _, iv := range merged {
		total += iv.end - iv.start
	}

	return total
}

func formatDuration(ns int64) string {
	d := time.Duration(ns)
	if d >= time.Second {
		return fmt.Sprintf("%.2fs", d.Seconds())
	}
	if d >= time.Millisecond {
		return fmt.Sprintf("%.2fms", float64(d.Nanoseconds())/1e6)
	}
	if d >= time.Microsecond {
		return fmt.Sprintf("%.1fµs", float64(d.Nanoseconds())/1e3)
	}

	return fmt.Sprintf("%dns", d.Nanoseconds())
}

// formatMicros formats a time.Duration with appropriate units (µs, ms, s)
func formatMicros(d time.Duration) string {
	if d >= time.Second {
		return fmt.Sprintf("%.2fs", d.Seconds())
	}
	if d >= time.Millisecond {
		return fmt.Sprintf("%.2fms", float64(d.Nanoseconds())/1e6)
	}
	if d >= time.Microsecond {
		return fmt.Sprintf("%.1fµs", float64(d.Nanoseconds())/1e3)
	}
	if d == 0 {
		return "0"
	}

	return fmt.Sprintf("%dns", d.Nanoseconds())
}

// getHostSpec gathers host system information
func getHostSpec() (cpu string, cores int, memGB int, osInfo string, hostname string) {
	cores = runtime.NumCPU()
	hostname, _ = os.Hostname()

	// Read CPU model from /proc/cpuinfo
	if f, err := os.Open("/proc/cpuinfo"); err == nil {
		defer f.Close()
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "model name") {
				parts := strings.SplitN(line, ":", 2)
				if len(parts) == 2 {
					cpu = strings.TrimSpace(parts[1])

					break
				}
			}
		}
	}
	if cpu == "" {
		cpu = runtime.GOARCH
	}

	// Read total memory from /proc/meminfo
	if f, err := os.Open("/proc/meminfo"); err == nil {
		defer f.Close()
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "MemTotal:") {
				var kb int64
				fmt.Sscanf(line, "MemTotal: %d kB", &kb)
				memGB = int(kb / 1024 / 1024)

				break
			}
		}
	}

	// Read OS info from /etc/os-release
	if f, err := os.Open("/etc/os-release"); err == nil {
		defer f.Close()
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			line := scanner.Text()
			if after, ok := strings.CutPrefix(line, "PRETTY_NAME="); ok {
				osInfo = strings.Trim(after, "\"")

				break
			}
		}
	}
	if osInfo == "" {
		osInfo = runtime.GOOS
	}

	return cpu, cores, memGB, osInfo, hostname
}

// isRuntimeFunc checks if a function name is a Go runtime/stdlib function
func isRuntimeFunc(name string) bool {
	return strings.HasPrefix(name, "runtime.") ||
		strings.HasPrefix(name, "sync.") ||
		strings.HasPrefix(name, "syscall.") ||
		strings.HasPrefix(name, "internal/") ||
		strings.Contains(name, "memmove") ||
		strings.Contains(name, "memclr") ||
		strings.Contains(name, "gcDrain") ||
		strings.Contains(name, "mallocgc")
}

// shortenFuncName shortens a function name for display
func shortenFuncName(name string) string {
	// Remove common prefixes
	name = strings.TrimPrefix(name, "github.com/e2b-dev/infra/packages/orchestrator/")
	name = strings.TrimPrefix(name, "github.com/e2b-dev/infra/packages/shared/")

	return name
}

// parseProfile parses a CPU profile and extracts hotspots with call stacks
func parseProfile(data []byte, numRuns int) ([]ProfileFunction, []ProfileCallStack) {
	if numRuns < 1 {
		numRuns = 1
	}
	prof, err := googleprof.ParseData(data)
	if err != nil {
		fmt.Printf("Warning: could not parse profile: %v\n", err)

		return nil, nil
	}

	// Get sample duration (nanoseconds per sample)
	sampleDuration := int64(10_000_000) // default 10ms
	if prof.Period > 0 {
		sampleDuration = prof.Period
	}

	// Build function stats with caller tracking
	type funcStats struct {
		flat    int64
		cum     int64
		callers map[string]int64 // caller -> samples
	}
	funcMap := make(map[string]*funcStats)
	var totalSamples int64

	// Track full call stacks
	type stackInfo struct {
		stack    []string
		samples  int64
		appFrame string // first non-runtime frame
	}
	stackMap := make(map[string]*stackInfo)

	for _, sample := range prof.Sample {
		value := sample.Value[0] // CPU time
		totalSamples += value

		// Build the call stack (reversed - caller first)
		var stack []string
		var appFrame string
		for i := len(sample.Location) - 1; i >= 0; i-- {
			loc := sample.Location[i]
			if len(loc.Line) > 0 {
				fn := loc.Line[0].Function
				if fn != nil {
					name := fn.Name
					stack = append(stack, shortenFuncName(name))
					if appFrame == "" && !isRuntimeFunc(name) {
						appFrame = shortenFuncName(name)
					}
				}
			}
		}

		// Track stack
		if len(stack) > 0 {
			stackKey := strings.Join(stack, " → ")
			if stackMap[stackKey] == nil {
				stackMap[stackKey] = &stackInfo{stack: stack, appFrame: appFrame}
			}
			stackMap[stackKey].samples += value
		}

		// Flat: only the top of the stack
		if len(sample.Location) > 0 {
			loc := sample.Location[0]
			if len(loc.Line) > 0 {
				fn := loc.Line[0].Function
				if fn != nil {
					name := fn.Name
					if funcMap[name] == nil {
						funcMap[name] = &funcStats{callers: make(map[string]int64)}
					}
					funcMap[name].flat += value

					// Track caller (second in stack)
					if len(sample.Location) > 1 {
						callerLoc := sample.Location[1]
						if len(callerLoc.Line) > 0 && callerLoc.Line[0].Function != nil {
							callerName := callerLoc.Line[0].Function.Name
							funcMap[name].callers[callerName] += value
						}
					}
				}
			}
		}

		// Cumulative: all functions in the stack
		for _, loc := range sample.Location {
			if len(loc.Line) > 0 {
				fn := loc.Line[0].Function
				if fn != nil {
					name := fn.Name
					if funcMap[name] == nil {
						funcMap[name] = &funcStats{callers: make(map[string]int64)}
					}
					funcMap[name].cum += value
				}
			}
		}
	}

	// Convert functions to slice and sort by flat time
	type funcEntry struct {
		name    string
		flat    int64
		cum     int64
		callers map[string]int64
	}
	var entries []funcEntry
	for name, stats := range funcMap {
		entries = append(entries, funcEntry{name, stats.flat, stats.cum, stats.callers})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].flat > entries[j].flat
	})

	// Convert to ProfileFunction, top 25
	var result []ProfileFunction
	maxEntries := min(len(entries), 25)

	for i := 0; i < maxEntries; i++ {
		e := entries[i]
		flatNs := e.flat * sampleDuration
		cumNs := e.cum * sampleDuration

		flatPct := float64(0)
		cumPct := float64(0)
		if totalSamples > 0 {
			flatPct = float64(e.flat) / float64(totalSamples) * 100
			cumPct = float64(e.cum) / float64(totalSamples) * 100
		}

		// Skip functions with < 0.5% flat time
		if flatPct < 0.5 {
			continue
		}

		// Get top 3 callers
		type callerEntry struct {
			name    string
			samples int64
		}
		var callerList []callerEntry
		for name, samples := range e.callers {
			callerList = append(callerList, callerEntry{name, samples})
		}
		sort.Slice(callerList, func(i, j int) bool {
			return callerList[i].samples > callerList[j].samples
		})

		var callers []string
		for j := 0; j < 3 && j < len(callerList); j++ {
			pct := float64(callerList[j].samples) / float64(e.flat) * 100
			callers = append(callers, fmt.Sprintf("%s (%.0f%%)", shortenFuncName(callerList[j].name), pct))
		}

		result = append(result, ProfileFunction{
			Name:    shortenFuncName(e.name),
			Flat:    formatDuration(flatNs),
			FlatAvg: formatDuration(flatNs / int64(numRuns)),
			FlatPct: fmt.Sprintf("%.1f%%", flatPct),
			Cum:     formatDuration(cumNs),
			CumPct:  fmt.Sprintf("%.1f%%", cumPct),
			Callers: callers,
		})
	}

	// Convert stacks to slice and sort by samples
	type stackEntry struct {
		key  string
		info *stackInfo
	}
	var stacks []stackEntry
	for key, info := range stackMap {
		stacks = append(stacks, stackEntry{key, info})
	}
	sort.Slice(stacks, func(i, j int) bool {
		return stacks[i].info.samples > stacks[j].info.samples
	})

	// Top 15 call stacks
	var callStacks []ProfileCallStack
	maxStacks := min(len(stacks), 15)
	for i := 0; i < maxStacks; i++ {
		s := stacks[i]
		flatNs := s.info.samples * sampleDuration
		flatPct := float64(0)
		if totalSamples > 0 {
			flatPct = float64(s.info.samples) / float64(totalSamples) * 100
		}

		// Skip stacks with < 1% time
		if flatPct < 1 {
			continue
		}

		// Shorten the stack for display (last 5 frames)
		displayStack := s.info.stack
		if len(displayStack) > 5 {
			displayStack = displayStack[len(displayStack)-5:]
		}

		callStacks = append(callStacks, ProfileCallStack{
			Stack:    strings.Join(displayStack, " → "),
			Flat:     formatDuration(flatNs),
			FlatAvg:  formatDuration(flatNs / int64(numRuns)),
			FlatPct:  fmt.Sprintf("%.1f%%", flatPct),
			AppFrame: s.info.appFrame,
		})
	}

	return result, callStacks
}

// formatBytes formats bytes into a human-readable string
func formatBytes(bytes int64) string {
	if bytes < 1024 {
		return fmt.Sprintf("%d B", bytes)
	}
	if bytes < 1024*1024 {
		return fmt.Sprintf("%.1f KB", float64(bytes)/1024)
	}
	if bytes < 1024*1024*1024 {
		return fmt.Sprintf("%.1f MB", float64(bytes)/(1024*1024))
	}

	return fmt.Sprintf("%.2f GB", float64(bytes)/(1024*1024*1024))
}

// parseHeapProfile parses a heap profile and extracts memory allocation hotspots
func parseHeapProfile(data []byte, numRuns int) ([]MemoryHotspot, int64, int64) {
	if numRuns < 1 {
		numRuns = 1
	}
	prof, err := googleprof.ParseData(data)
	if err != nil {
		fmt.Printf("Warning: could not parse heap profile: %v\n", err)

		return nil, 0, 0
	}

	// Heap profiles have multiple sample types:
	// 0: alloc_objects - number of objects allocated
	// 1: alloc_space - bytes allocated
	// 2: inuse_objects - objects currently in use
	// 3: inuse_space - bytes currently in use
	// We want alloc_space (index 1) for total allocations

	allocSpaceIdx := 1
	allocObjectsIdx := 0

	// Find the correct indices by name
	for i, st := range prof.SampleType {
		switch st.Type {
		case "alloc_space":
			allocSpaceIdx = i
		case "alloc_objects":
			allocObjectsIdx = i
		}
	}

	// Build function allocation stats
	type allocStats struct {
		bytes   int64
		objects int64
		callers map[string]int64 // caller -> bytes
	}
	funcMap := make(map[string]*allocStats)
	var totalBytes, totalObjects int64

	for _, sample := range prof.Sample {
		bytes := int64(0)
		objects := int64(0)
		if len(sample.Value) > allocSpaceIdx {
			bytes = sample.Value[allocSpaceIdx]
		}
		if len(sample.Value) > allocObjectsIdx {
			objects = sample.Value[allocObjectsIdx]
		}

		totalBytes += bytes
		totalObjects += objects

		if bytes == 0 {
			continue
		}

		// Process stack: attribute to leaf function, track callers
		if len(sample.Location) > 0 {
			// Leaf function (last in stack = index 0)
			leafLoc := sample.Location[0]
			if len(leafLoc.Line) > 0 && leafLoc.Line[0].Function != nil {
				leafName := leafLoc.Line[0].Function.Name

				if funcMap[leafName] == nil {
					funcMap[leafName] = &allocStats{callers: make(map[string]int64)}
				}
				funcMap[leafName].bytes += bytes
				funcMap[leafName].objects += objects

				// Track caller (if any)
				if len(sample.Location) > 1 {
					callerLoc := sample.Location[1]
					if len(callerLoc.Line) > 0 && callerLoc.Line[0].Function != nil {
						callerName := callerLoc.Line[0].Function.Name
						funcMap[leafName].callers[callerName] += bytes
					}
				}
			}
		}
	}

	// Sort by bytes allocated
	type allocEntry struct {
		name    string
		bytes   int64
		objects int64
		callers map[string]int64
	}
	var entries []allocEntry
	for name, stats := range funcMap {
		entries = append(entries, allocEntry{name, stats.bytes, stats.objects, stats.callers})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].bytes > entries[j].bytes
	})

	// Convert to MemoryHotspot, top 20
	var result []MemoryHotspot
	maxEntries := min(len(entries), 20)

	for i := 0; i < maxEntries; i++ {
		e := entries[i]
		allocPct := float64(0)
		if totalBytes > 0 {
			allocPct = float64(e.bytes) / float64(totalBytes) * 100
		}

		// Skip functions with < 0.5% allocations
		if allocPct < 0.5 {
			continue
		}

		// Get top 3 callers
		type callerEntry struct {
			name  string
			bytes int64
		}
		var callerList []callerEntry
		for name, b := range e.callers {
			callerList = append(callerList, callerEntry{name, b})
		}
		sort.Slice(callerList, func(i, j int) bool {
			return callerList[i].bytes > callerList[j].bytes
		})

		var callers []string
		for j := 0; j < 3 && j < len(callerList); j++ {
			pct := float64(callerList[j].bytes) / float64(e.bytes) * 100
			callers = append(callers, fmt.Sprintf("%s (%.0f%%)", shortenFuncName(callerList[j].name), pct))
		}

		result = append(result, MemoryHotspot{
			Name:       shortenFuncName(e.name),
			AllocBytes: formatBytes(e.bytes),
			AllocAvg:   formatBytes(e.bytes / int64(numRuns)),
			AllocPct:   fmt.Sprintf("%.1f%%", allocPct),
			AllocObjs:  fmt.Sprintf("%d", e.objects),
			Callers:    callers,
		})
	}

	return result, totalBytes, totalObjects
}

func run(ctx context.Context, buildID, kernel, fcVer string, vcpu, memory, disk int64, count int, traceEnabled bool, pprofEnabled bool, pprofPort int) error {
	l, _ := logger.NewDevelopmentLogger()
	sbxlogger.SetSandboxLoggerInternal(l)

	config, err := cfg.Parse()
	if err != nil {
		return err
	}

	slotStorage, err := network.NewStorageLocal(ctx, config.NetworkConfig)
	if err != nil {
		return err
	}
	networkPool := network.NewPool(8, 8, slotStorage, config.NetworkConfig)
	go networkPool.Populate(ctx)
	defer networkPool.Close(context.Background())

	devicePool, err := nbd.NewDevicePool()
	if err != nil {
		return fmt.Errorf("nbd pool (modprobe nbd?): %w", err)
	}
	go devicePool.Populate(ctx)
	defer devicePool.Close(context.Background())

	flags, _ := featureflags.NewClient()

	persistence, _ := storage.GetTemplateStorageProvider(ctx, nil)
	blockMetrics, _ := blockmetrics.NewMetrics(&noop.MeterProvider{})

	cache, err := template.NewCache(config, flags, persistence, blockMetrics)
	if err != nil {
		return err
	}
	cache.Start(ctx)
	defer cache.Stop()

	factory := sandbox.NewFactory(config.BuilderConfig, networkPool, devicePool, flags)

	fmt.Printf("📦 Loading %s...\n", buildID)
	tmpl, err := cache.GetTemplate(ctx, buildID, false, false)
	if err != nil {
		return err
	}

	token := "local"
	r := &runner{
		ctx:           ctx,
		factory:       factory,
		tmpl:          tmpl,
		templateCache: cache,
		buildID:       buildID,
		trace:         traceEnabled,
		pprofEnabled:  pprofEnabled,
		pprofPort:     pprofPort,
		spec: func() SpecInfo {
			hostCPU, hostCores, hostMemGB, hostOS, hostname := getHostSpec()

			return SpecInfo{
				TemplateID:   buildID,
				VCPUs:        vcpu,
				MemoryMB:     memory,
				DiskMB:       disk,
				Kernel:       kernel,
				FCVersion:    fcVer,
				HostCPU:      hostCPU,
				HostCores:    hostCores,
				HostMemoryGB: hostMemGB,
				HostOS:       hostOS,
				Hostname:     hostname,
			}
		}(),
		sbxConfig: sandbox.Config{
			BaseTemplateID: buildID, Vcpu: vcpu, RamMB: memory, TotalDiskSizeMB: disk,
			Network:           &orchestrator.SandboxNetworkConfig{},
			Envd:              sandbox.EnvdMetadata{Vars: map[string]string{}, AccessToken: &token, Version: "1.0.0"},
			FirecrackerConfig: fc.Config{KernelVersion: kernel, FirecrackerVersion: fcVer},
			TraceEnabled:      traceEnabled,
		},
	}

	if traceEnabled {
		fmt.Println("Page fault tracing enabled")
	}
	if pprofEnabled {
		// Start pprof HTTP server
		go func() {
			addr := fmt.Sprintf("localhost:%d", pprofPort)
			fmt.Printf("pprof server: http://%s/debug/pprof/\n", addr)
			if err := http.ListenAndServe(addr, nil); err != nil {
				fmt.Printf("pprof server error: %v\n", err)
			}
		}()
	}

	if count > 0 {
		return r.runBenchmark(count)
	}

	return r.runInteractive()
}
