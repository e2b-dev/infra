package telemetry

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

type (
	CounterType                 string
	ObservableCounterType       string
	GaugeFloatType              string
	GaugeIntType                string
	UpDownCounterType           string
	ObservableUpDownCounterType string
	HistogramType               string
)

const (
	ApiOrchestratorCreatedSandboxes      CounterType = "api.orchestrator.created_sandboxes"
	ApiOrchestratorResumeOriginNodeRemap CounterType = "api.orchestrator.resume_origin_node_remapped"
	SandboxCreateMeterName               CounterType = "api.env.instance.started"

	TeamSandboxCreated CounterType = "e2b.team.sandbox.created"

	EnvdInitCalls CounterType = "orchestrator.sandbox.envd.init.calls"

	// 2 MiB chunks the pre-pause envd heap collapse attempted, split by the
	// result attribute (collapsed|skipped): attempts = total, successful =
	// collapsed.
	EnvdCollapseChunks CounterType = "orchestrator.sandbox.envd.collapse.chunks"
	// Incremented by the balance_dirty_pages thread count at every 200 ms poll
	// for the lifetime of the process. rate() shows dirty-page throttle
	// intensity in real-time; 0 when no stalls are occurring.
	OrchestratorHostBalanceDirtyPagesThreads CounterType = "orchestrator.host.balance_dirty_pages.threads"

	OrchestratorSandboxKilledCounterName CounterType = "orchestrator.sandbox.killed"

	// OrchestratorSnapshotUploadFailedCounterName counts pause-snapshot uploads
	// that never landed durably (budget exhausted or a non-retryable error).
	// A non-zero rate means lost snapshots.
	OrchestratorSnapshotUploadFailedCounterName CounterType = "orchestrator.snapshot.upload.failed"

	// PauseResumePrefetchHarvestAttempts counts pause-resume prefetch harvest
	// attempts, by result (success|resume_failed|collect_failed|skipped). The
	// throwaway is absent from Prometheus otherwise (registration-skip), so this
	// is the harvest-activity / failure-rate signal.
	PauseResumePrefetchHarvestAttempts CounterType = "orchestrator.sandbox.pause_resume_prefetch.harvest.attempts"

	ApiRedisStoragePublisherPublished CounterType = "api.redis_storage.publisher.published"
	ApiRedisStoragePublisherDropped   CounterType = "api.redis_storage.publisher.dropped"

	// ApiRedisStorageExpirationIndexHealed counts sandboxes the healer re-added
	// to the global expiration index. Healthy steady state is zero; a sustained
	// non-zero rate means expiration index writes are being lost and sandboxes
	// would otherwise become invisible to the evictor (immortal).
	ApiRedisStorageExpirationIndexHealed CounterType = "api.redis_storage.expiration_index.healed"
	// ApiRedisStorageExpirationIndexSwept counts members removed from the
	// global expiration index by the evictor scan
	// (reason=orphan|dead_execution|invalid).
	ApiRedisStorageExpirationIndexSwept CounterType = "api.redis_storage.expiration_index.swept"
	// ApiRedisStorageExpirationIndexRescored counts live members whose index
	// score drifted from the stored EndTime and were re-scored by the evictor
	// scan. Sustained non-zero rate means score updates are being lost.
	ApiRedisStorageExpirationIndexRescored CounterType = "api.redis_storage.expiration_index.rescored"
)

const (
	ApiOrchestratorSbxCreateSuccess ObservableCounterType = "api.orchestrator.sandbox.create.success"
	ApiOrchestratorSbxCreateFailure ObservableCounterType = "api.orchestrator.sandbox.create.failure"
)

const (
	OrchestratorSandboxCountMeterName ObservableUpDownCounterType = "orchestrator.env.sandbox.running"

	ClientProxyServerConnectionsMeterCounterName ObservableUpDownCounterType = "client_proxy.proxy.server.connections.open"
	ClientProxyPoolConnectionsMeterCounterName   ObservableUpDownCounterType = "client_proxy.proxy.pool.connections.open"
	ClientProxyPoolSizeMeterCounterName          ObservableUpDownCounterType = "client_proxy.proxy.pool.size"

	OrchestratorProxyServerConnectionsMeterCounterName ObservableUpDownCounterType = "orchestrator.proxy.server.connections.open"
	OrchestratorProxyPoolConnectionsMeterCounterName   ObservableUpDownCounterType = "orchestrator.proxy.pool.connections.open"
	OrchestratorProxyPoolSizeMeterCounterName          ObservableUpDownCounterType = "orchestrator.proxy.pool.size"

	BuildCounterMeterName       ObservableUpDownCounterType = "api.env.build.running"
	EvictionsRunningCounterName ObservableUpDownCounterType = "api.evictor.evictions.running"

	TCPFirewallActiveConnections ObservableUpDownCounterType = "orchestrator.tcpfirewall.connections.active"

	ApiRedisStoragePublisherQueueDepth ObservableUpDownCounterType = "api.redis_storage.publisher.queue.depth"
)

const (
	SandboxCpuUsedGaugeName GaugeFloatType = "e2b.sandbox.cpu.used"
)

const (
	// Build timing histograms
	BuildDurationHistogramName      HistogramType = "template.build.duration"
	BuildPhaseDurationHistogramName HistogramType = "template.build.phase.duration"
	BuildStepDurationHistogramName  HistogramType = "template.build.step.duration"

	// Sandbox timing histograms
	OrchestratorSandboxCreateDurationName HistogramType = "orchestrator.sandbox.create.duration"
	WaitForEnvdDurationHistogramName      HistogramType = "orchestrator.sandbox.envd.init.duration"
	GuestSyncDurationHistogramName        HistogramType = "orchestrator.sandbox.guest_sync.duration"

	// Pre-pause envd heap collapse round-trip duration (the pause-path cost of
	// POST /collapse: network plus envd's madvise work), recorded once per pause
	// when the collapse-envd-heap flag is on.
	EnvdCollapseDurationHistogramName HistogramType = "orchestrator.sandbox.envd.collapse.duration"

	// Pause-resume prefetch harvest cost, recorded once per harvest attempt:
	// duration is the whole throwaway resume-and-persist run (slot-hold cost);
	// pages is the harvested trace size (distinct 2 MiB blocks), recorded only on
	// success, so its bottom bucket surfaces the empty-trace (idle-at-pause) rate.
	PauseResumePrefetchHarvestDurationName HistogramType = "orchestrator.sandbox.pause_resume_prefetch.harvest.duration"
	PauseResumePrefetchHarvestPagesName    HistogramType = "orchestrator.sandbox.pause_resume_prefetch.harvest.pages"

	// Sandbox startup working-set histograms: demand-fault pages/bytes a guest
	// needed to reach a successful envd init, recorded once per start. Sampled
	// per start (not per fault), so histogram_quantile yields per-sandbox
	// percentiles.
	UffdStartupPagesHistogramName       HistogramType = "orchestrator.sandbox.uffd.startup.pages"
	UffdStartupSourcePagesHistogramName HistogramType = "orchestrator.sandbox.uffd.startup.source_pages"
	UffdStartupBytesHistogramName       HistogramType = "orchestrator.sandbox.uffd.startup.bytes"

	// TCP Firewall histograms
	TCPFirewallConnectionDurationHistogramName    HistogramType = "orchestrator.tcpfirewall.connection.duration"
	TCPFirewallConnectionsPerSandboxHistogramName HistogramType = "orchestrator.tcpfirewall.connections.per_sandbox"

	// Ingress proxy histograms
	IngressProxyConnectionDurationHistogramName    HistogramType = "orchestrator.proxy.connection.duration"
	IngressProxyConnectionsPerSandboxHistogramName HistogramType = "orchestrator.proxy.connections.per_sandbox"
)

const (
	// Build result counters
	BuildResultCounterName      CounterType = "template.build.result"
	BuildCacheResultCounterName CounterType = "template.build.cache.result"

	// TCP Firewall counters
	TCPFirewallConnectionsTotal CounterType = "orchestrator.tcpfirewall.connections.total"
	TCPFirewallErrorsTotal      CounterType = "orchestrator.tcpfirewall.errors.total"
	TCPFirewallDecisionsTotal   CounterType = "orchestrator.tcpfirewall.decisions.total"

	// Ingress proxy counters
	IngressProxyConnectionsBlockedTotal CounterType = "orchestrator.proxy.connections.blocked.total"

	// cmux counters
	CmuxErrorsTotal CounterType = "orchestrator.cmux.errors.total"

	// Firecracker net counters — global totals, no sandbox_id (low cardinality).
	// All carry a direction=tx/rx attribute. Per-sandbox distributions are histograms below.
	SandboxFCNetFails         CounterType = "orchestrator.sandbox.fc.net.fails"
	SandboxFCNetNoAvailBuffer CounterType = "orchestrator.sandbox.fc.net.no_avail_buffer"
	SandboxFCNetTapIOFails    CounterType = "orchestrator.sandbox.fc.net.tap_io_fails"

	// Firecracker block counters — global totals, no sandbox_id (low cardinality).
	// Carry a direction=read/write attribute where applicable.
	SandboxFCBlockFails         CounterType = "orchestrator.sandbox.fc.block.fails"
	SandboxFCBlockNoAvailBuffer CounterType = "orchestrator.sandbox.fc.block.no_avail_buffer"
)

const (
	ApiRedisStoragePublisherPublishDuration HistogramType = "api.redis_storage.publisher.publish.duration"

	// Firecracker net histograms — per-sandbox distribution per metrics flush, no sandbox_id.
	// Firecracker serializes SharedIncMetric as per-flush deltas (default flush interval: 60 s).
	// Symmetric TX/RX metrics carry a direction=tx/rx attribute; TX-only metrics always use direction=tx.
	SandboxFCNetBytes                HistogramType = "orchestrator.sandbox.fc.net.bytes"
	SandboxFCNetPackets              HistogramType = "orchestrator.sandbox.fc.net.packets"
	SandboxFCNetCount                HistogramType = "orchestrator.sandbox.fc.net.count"
	SandboxFCNetRateLimiterThrottled HistogramType = "orchestrator.sandbox.fc.net.rate_limiter_throttled"
	// TX-only: no RX equivalent in Firecracker metrics.
	SandboxFCNetRateLimiterEventCount HistogramType = "orchestrator.sandbox.fc.net.rate_limiter_event_count"
	SandboxFCNetRemainingReqs         HistogramType = "orchestrator.sandbox.fc.net.remaining_reqs"

	// Firecracker block histograms — per-sandbox distribution per metrics flush, no sandbox_id.
	// Symmetric read/write metrics carry a direction=read/write attribute.
	SandboxFCBlockBytes                 HistogramType = "orchestrator.sandbox.fc.block.bytes"
	SandboxFCBlockCount                 HistogramType = "orchestrator.sandbox.fc.block.count"
	SandboxFCBlockRateLimiterThrottled  HistogramType = "orchestrator.sandbox.fc.block.rate_limiter_throttled"
	SandboxFCBlockRateLimiterEventCount HistogramType = "orchestrator.sandbox.fc.block.rate_limiter_event_count"
	SandboxFCBlockIOEngineThrottled     HistogramType = "orchestrator.sandbox.fc.block.io_engine_throttled"
	SandboxFCBlockRemainingReqs         HistogramType = "orchestrator.sandbox.fc.block.remaining_reqs"

	SnapshotDiffBytes  HistogramType = "orchestrator.sandbox.snapshot.diff.bytes"
	SnapshotDiffRatio  HistogramType = "orchestrator.sandbox.snapshot.diff.ratio"
	SnapshotTotalBytes HistogramType = "orchestrator.sandbox.snapshot.total.bytes"

	UploadUncompressedBytes HistogramType = "orchestrator.sandbox.upload.uncompressed.bytes"
	UploadCompressedBytes   HistogramType = "orchestrator.sandbox.upload.compressed.bytes"
	UploadCompressionRatio  HistogramType = "orchestrator.sandbox.upload.compression.ratio"
)

const (
	ApiOrchestratorCountMeterName GaugeIntType = "api.orchestrator.status"
	OrchestratorStatusGaugeName   GaugeIntType = "orchestrator.status"

	// Orchestrator node resources allocated to running sandboxes (sum across running sandboxes)
	OrchestratorCpuAllocatedGaugeName    GaugeIntType = "orchestrator.sandbox.cpu.allocated"
	OrchestratorMemoryAllocatedGaugeName GaugeIntType = "orchestrator.sandbox.memory.allocated"
	OrchestratorDiskAllocatedGaugeName   GaugeIntType = "orchestrator.sandbox.disk.allocated"

	// Sandbox metrics
	SandboxRamUsedGaugeName   GaugeIntType = "e2b.sandbox.ram.used"
	SandboxRamTotalGaugeName  GaugeIntType = "e2b.sandbox.ram.total"
	SandboxRamCacheGaugeName  GaugeIntType = "e2b.sandbox.ram.cache"
	SandboxCpuTotalGaugeName  GaugeIntType = "e2b.sandbox.cpu.total"
	SandboxDiskUsedGaugeName  GaugeIntType = "e2b.sandbox.disk.used"
	SandboxDiskTotalGaugeName GaugeIntType = "e2b.sandbox.disk.total"

	// Team metrics
	TeamSandboxRunningGaugeName GaugeIntType = "e2b.team.sandbox.running"

	SandboxCountGaugeName GaugeIntType = "api.env.instance.running"

	// Build resource metrics
	BuildRootfsSizeHistogramName HistogramType = "template.build.rootfs.size"
)

var counterDesc = map[CounterType]string{
	SandboxCreateMeterName:                      "Number of currently waiting requests to create a new sandbox",
	ApiOrchestratorCreatedSandboxes:             "Number of successfully created sandboxes",
	ApiOrchestratorResumeOriginNodeRemap:        "Number of resume snapshots repointed to the fallback node a previous resume timed out on",
	BuildResultCounterName:                      "Number of template build results",
	BuildCacheResultCounterName:                 "Number of build cache results",
	TeamSandboxCreated:                          "Counter of started sandboxes for the team in the interval",
	OrchestratorHostBalanceDirtyPagesThreads:    "Cumulative stalled thread-polls during sandbox resume; rate() gives throttle intensity",
	EnvdInitCalls:                               "Number of envd initialization calls",
	EnvdCollapseChunks:                          "2 MiB chunks the pre-pause envd heap collapse attempted, by result",
	OrchestratorSandboxKilledCounterName:        "Number of sandboxes killed, labeled by kill reason",
	OrchestratorSnapshotUploadFailedCounterName: "Number of pause-snapshot uploads that never landed durably",
	PauseResumePrefetchHarvestAttempts:          "Pause-resume prefetch harvest attempts, by result",
	TCPFirewallConnectionsTotal:                 "Total number of TCP firewall connections processed",
	TCPFirewallErrorsTotal:                      "Total number of TCP firewall errors",
	TCPFirewallDecisionsTotal:                   "Total number of TCP firewall allow/block decisions",

	IngressProxyConnectionsBlockedTotal: "Total number of ingress proxy connections blocked by connection limit",
	CmuxErrorsTotal:                     "Total number of cmux connection multiplexer errors",

	SandboxFCNetFails:         "Total Firecracker VMM errors transmitting or receiving data (direction=tx/rx)",
	SandboxFCNetNoAvailBuffer: "Total Firecracker VMM events where no virtqueue buffer was available (direction=tx/rx)",
	SandboxFCNetTapIOFails:    "Total Firecracker VMM TAP I/O failures (direction=tx/rx)",

	SandboxFCBlockFails:         "Total Firecracker VMM block device execution/event failures",
	SandboxFCBlockNoAvailBuffer: "Total Firecracker VMM block events where no virtqueue buffer was available",

	ApiRedisStoragePublisherPublished: "Total Redis PUBLISH calls completed by the storage publisher (result=success|failure)",
	ApiRedisStoragePublisherDropped:   "Total storage notifications dropped before reaching Redis (reason=queue_full|closed)",

	ApiRedisStorageExpirationIndexHealed:   "Sandboxes re-added to the global expiration index by the healer; sustained non-zero rate means index writes are being lost",
	ApiRedisStorageExpirationIndexSwept:    "Members removed from the global expiration index by the evictor scan (reason=orphan|dead_execution|invalid)",
	ApiRedisStorageExpirationIndexRescored: "Live expiration index members re-scored after drifting from the stored EndTime",
}

var counterUnits = map[CounterType]string{
	SandboxCreateMeterName:                      "{sandbox}",
	ApiOrchestratorCreatedSandboxes:             "{sandbox}",
	ApiOrchestratorResumeOriginNodeRemap:        "{snapshot}",
	BuildResultCounterName:                      "{build}",
	BuildCacheResultCounterName:                 "{layer}",
	TeamSandboxCreated:                          "{sandbox}",
	OrchestratorHostBalanceDirtyPagesThreads:    "{thread}",
	EnvdInitCalls:                               "1",
	EnvdCollapseChunks:                          "{chunk}",
	OrchestratorSandboxKilledCounterName:        "{sandbox}",
	OrchestratorSnapshotUploadFailedCounterName: "{snapshot}",
	PauseResumePrefetchHarvestAttempts:          "{attempt}",
	TCPFirewallConnectionsTotal:                 "{connection}",
	TCPFirewallErrorsTotal:                      "{error}",
	TCPFirewallDecisionsTotal:                   "{decision}",

	IngressProxyConnectionsBlockedTotal: "{connection}",
	CmuxErrorsTotal:                     "{error}",

	SandboxFCNetFails:         "{error}",
	SandboxFCNetNoAvailBuffer: "{event}",
	SandboxFCNetTapIOFails:    "{error}",

	SandboxFCBlockFails:         "{error}",
	SandboxFCBlockNoAvailBuffer: "{event}",

	ApiRedisStoragePublisherPublished: "{notification}",
	ApiRedisStoragePublisherDropped:   "{notification}",

	ApiRedisStorageExpirationIndexHealed:   "{sandbox}",
	ApiRedisStorageExpirationIndexSwept:    "{member}",
	ApiRedisStorageExpirationIndexRescored: "{member}",
}

var observableCounterDesc = map[ObservableCounterType]string{
	ApiOrchestratorSbxCreateSuccess: "Counter of successful sandbox creation requests.",
	ApiOrchestratorSbxCreateFailure: "Counter of failed sandbox creation requests.",
}

var observableCounterUnits = map[ObservableCounterType]string{
	ApiOrchestratorSbxCreateSuccess: "{sandbox}",
	ApiOrchestratorSbxCreateFailure: "{sandbox}",
}

var upDownCounterDesc = map[UpDownCounterType]string{}

var upDownCounterUnits = map[UpDownCounterType]string{}

var observableUpDownCounterDesc = map[ObservableUpDownCounterType]string{
	OrchestratorSandboxCountMeterName:                  "Counter of running sandboxes on the orchestrator.",
	ClientProxyServerConnectionsMeterCounterName:       "Open connections to the client proxy from load balancer.",
	ClientProxyPoolConnectionsMeterCounterName:         "Open connections from the client proxy to the orchestrator proxy.",
	ClientProxyPoolSizeMeterCounterName:                "Size of the client proxy pool.",
	OrchestratorProxyServerConnectionsMeterCounterName: "Open connections to the orchestrator proxy from client proxies.",
	OrchestratorProxyPoolConnectionsMeterCounterName:   "Open connections from the orchestrator proxy to sandboxes.",
	OrchestratorProxyPoolSizeMeterCounterName:          "Size of the orchestrator proxy pool.",
	BuildCounterMeterName:                              "Counter of running builds.",
	EvictionsRunningCounterName:                        "Counter of currently running evictions.",

	TCPFirewallActiveConnections: "Number of currently active TCP firewall connections.",

	ApiRedisStoragePublisherQueueDepth: "Current depth of the Redis storage publisher queue (items awaiting PUBLISH).",
}

var observableUpDownCounterUnits = map[ObservableUpDownCounterType]string{
	OrchestratorSandboxCountMeterName:                  "{sandbox}",
	ClientProxyServerConnectionsMeterCounterName:       "{connection}",
	ClientProxyPoolConnectionsMeterCounterName:         "{connection}",
	ClientProxyPoolSizeMeterCounterName:                "{transport}",
	OrchestratorProxyServerConnectionsMeterCounterName: "{connection}",
	OrchestratorProxyPoolConnectionsMeterCounterName:   "{connection}",
	OrchestratorProxyPoolSizeMeterCounterName:          "{transport}",
	BuildCounterMeterName:                              "{build}",
	EvictionsRunningCounterName:                        "{eviction}",

	TCPFirewallActiveConnections: "{connection}",

	ApiRedisStoragePublisherQueueDepth: "{notification}",
}

var gaugeFloatDesc = map[GaugeFloatType]string{
	SandboxCpuUsedGaugeName: "Amount of CPU used by the sandbox.",
}

var gaugeFloatUnits = map[GaugeFloatType]string{
	SandboxCpuUsedGaugeName: "{percent}",
}

var gaugeIntDesc = map[GaugeIntType]string{
	ApiOrchestratorCountMeterName:        "Counter of running orchestrators.",
	OrchestratorStatusGaugeName:          "Self-reported orchestrator status (always 1, labelled with status and version).",
	OrchestratorCpuAllocatedGaugeName:    "Total vCPUs allocated to running sandboxes on the orchestrator node.",
	OrchestratorMemoryAllocatedGaugeName: "Total memory allocated to running sandboxes on the orchestrator node.",
	OrchestratorDiskAllocatedGaugeName:   "Total disk space allocated to running sandboxes on the orchestrator node.",
	SandboxRamUsedGaugeName:              "Amount of RAM used by the sandbox.",
	SandboxRamTotalGaugeName:             "Amount of RAM available to the sandbox.",
	SandboxRamCacheGaugeName:             "Amount of RAM used by the page cache in the sandbox.",
	SandboxCpuTotalGaugeName:             "Amount of CPU available to the sandbox.",
	SandboxDiskUsedGaugeName:             "Amount of disk space used by the sandbox.",
	SandboxDiskTotalGaugeName:            "Amount of disk space available to the sandbox.",
	TeamSandboxRunningGaugeName:          "The number of sandboxes running for the team in the interval.",
	SandboxCountGaugeName:                "Number of running sandbox instances per team.",
}

var gaugeIntUnits = map[GaugeIntType]string{
	ApiOrchestratorCountMeterName:        "{orchestrator}",
	OrchestratorStatusGaugeName:          "{orchestrator}",
	OrchestratorCpuAllocatedGaugeName:    "{count}",
	OrchestratorMemoryAllocatedGaugeName: "{By}",
	OrchestratorDiskAllocatedGaugeName:   "{By}",
	SandboxRamUsedGaugeName:              "{By}",
	SandboxRamTotalGaugeName:             "{By}",
	SandboxRamCacheGaugeName:             "{By}",
	SandboxCpuTotalGaugeName:             "{count}",
	SandboxDiskUsedGaugeName:             "{By}",
	SandboxDiskTotalGaugeName:            "{By}",
	TeamSandboxRunningGaugeName:          "{sandbox}",
	SandboxCountGaugeName:                "{sandbox}",
}

func GetCounter(meter metric.Meter, name CounterType) (metric.Int64Counter, error) {
	desc := counterDesc[name]
	unit := counterUnits[name]

	return meter.Int64Counter(string(name),
		metric.WithDescription(desc),
		metric.WithUnit(unit),
	)
}

func GetUpDownCounter(meter metric.Meter, name UpDownCounterType) (metric.Int64UpDownCounter, error) {
	desc := upDownCounterDesc[name]
	unit := upDownCounterUnits[name]

	return meter.Int64UpDownCounter(string(name),
		metric.WithDescription(desc),
		metric.WithUnit(unit),
	)
}

func GetObservableCounter(meter metric.Meter, name ObservableCounterType, callback metric.Int64Callback) (metric.Int64ObservableCounter, error) {
	desc := observableCounterDesc[name]
	unit := observableCounterUnits[name]

	return meter.Int64ObservableCounter(string(name),
		metric.WithDescription(desc),
		metric.WithUnit(unit),
		metric.WithInt64Callback(callback),
	)
}

func GetObservableUpDownCounter(meter metric.Meter, name ObservableUpDownCounterType, callback metric.Int64Callback) (metric.Int64ObservableUpDownCounter, error) {
	desc := observableUpDownCounterDesc[name]
	unit := observableUpDownCounterUnits[name]

	return meter.Int64ObservableUpDownCounter(string(name),
		metric.WithDescription(desc),
		metric.WithUnit(unit),
		metric.WithInt64Callback(callback),
	)
}

func GetGaugeFloat(meter metric.Meter, name GaugeFloatType) (metric.Float64ObservableGauge, error) {
	desc := gaugeFloatDesc[name]
	unit := gaugeFloatUnits[name]

	return meter.Float64ObservableGauge(string(name),
		metric.WithDescription(desc),
		metric.WithUnit(unit),
	)
}

func GetGaugeInt(meter metric.Meter, name GaugeIntType) (metric.Int64ObservableGauge, error) {
	desc := gaugeIntDesc[name]
	unit := gaugeIntUnits[name]

	return meter.Int64ObservableGauge(string(name),
		metric.WithDescription(desc),
		metric.WithUnit(unit),
	)
}

var histogramDesc = map[HistogramType]string{
	ApiRedisStoragePublisherPublishDuration: "Duration of a single Redis PUBLISH round-trip from the storage publisher",

	BuildDurationHistogramName:            "Time taken to build a template",
	BuildPhaseDurationHistogramName:       "Time taken to build each phase of a template",
	BuildStepDurationHistogramName:        "Time taken to build each step of a template",
	BuildRootfsSizeHistogramName:          "Size of the built template rootfs in bytes",
	OrchestratorSandboxCreateDurationName: "Time taken to create a sandbox",
	WaitForEnvdDurationHistogramName:      "Time taken for Envd to initialize successfully",
	EnvdCollapseDurationHistogramName:     "Time taken for the pre-pause envd heap collapse round-trip",
	GuestSyncDurationHistogramName:        "Time taken for the mandatory pre-pause guest sync (filesystem-only pause)",

	PauseResumePrefetchHarvestDurationName: "Time taken for a pause-resume prefetch harvest run (slot-hold cost)",
	PauseResumePrefetchHarvestPagesName:    "Harvested resume-prefetch trace size in 2 MiB blocks, per successful harvest",

	UffdStartupPagesHistogramName:       "Demand-fault pages a guest needed to reach a successful envd init, per start",
	UffdStartupSourcePagesHistogramName: "Subset of startup demand-fault pages pulled from the source (e.g. GCS), per start",
	UffdStartupBytesHistogramName:       "Bytes faulted into a guest to reach a successful envd init, per start",

	TCPFirewallConnectionDurationHistogramName:    "Duration of TCP firewall proxied connections",
	TCPFirewallConnectionsPerSandboxHistogramName: "Number of active TCP firewall connections per sandbox",

	IngressProxyConnectionDurationHistogramName:    "Duration of ingress proxy connections",
	IngressProxyConnectionsPerSandboxHistogramName: "Number of active ingress proxy connections per sandbox",

	// Firecracker net histograms (direction=tx/rx attribute; TX-only carry direction=tx)
	SandboxFCNetBytes:                 "Distribution of Firecracker VMM bytes per metrics flush",
	SandboxFCNetPackets:               "Distribution of Firecracker VMM packets per metrics flush",
	SandboxFCNetCount:                 "Distribution of Firecracker VMM I/O operations per metrics flush",
	SandboxFCNetRateLimiterThrottled:  "Distribution of Firecracker VMM ops throttled by rate limiter per metrics flush",
	SandboxFCNetRateLimiterEventCount: "Distribution of Firecracker VMM TX rate limiter events per metrics flush",
	SandboxFCNetRemainingReqs:         "Distribution of Firecracker VMM TX queue remaining-request events per metrics flush",

	// Firecracker block histograms (direction=read/write attribute)
	SandboxFCBlockBytes:                 "Distribution of Firecracker VMM block bytes per metrics flush",
	SandboxFCBlockCount:                 "Distribution of Firecracker VMM block I/O operations per metrics flush",
	SandboxFCBlockRateLimiterThrottled:  "Distribution of Firecracker VMM block ops throttled by rate limiter per metrics flush",
	SandboxFCBlockRateLimiterEventCount: "Distribution of Firecracker VMM block rate limiter events per metrics flush",
	SandboxFCBlockIOEngineThrottled:     "Distribution of Firecracker VMM block ops throttled by io_uring engine per metrics flush",
	SandboxFCBlockRemainingReqs:         "Distribution of Firecracker VMM block queue remaining-request events per metrics flush",

	SnapshotDiffBytes:  "Per-snapshot dirty/empty bytes per file",
	SnapshotDiffRatio:  "Per-snapshot dirty/empty as fraction of total mapped size (1.0 = 100%)",
	SnapshotTotalBytes: "Per-snapshot total mapped size of the file",

	UploadUncompressedBytes: "Per-upload uncompressed artifact size",
	UploadCompressedBytes:   "Per-upload compressed artifact size",
	UploadCompressionRatio:  "Per-upload compressed/uncompressed ratio (1.0 = no compression)",
}

var histogramUnits = map[HistogramType]string{
	ApiRedisStoragePublisherPublishDuration: "ms",

	BuildDurationHistogramName:                    "ms",
	BuildPhaseDurationHistogramName:               "ms",
	BuildStepDurationHistogramName:                "ms",
	BuildRootfsSizeHistogramName:                  "{By}",
	OrchestratorSandboxCreateDurationName:         "ms",
	WaitForEnvdDurationHistogramName:              "ms",
	EnvdCollapseDurationHistogramName:             "ms",
	GuestSyncDurationHistogramName:                "ms",
	PauseResumePrefetchHarvestDurationName:        "ms",
	PauseResumePrefetchHarvestPagesName:           "{page}",
	UffdStartupPagesHistogramName:                 "{page}",
	UffdStartupSourcePagesHistogramName:           "{page}",
	UffdStartupBytesHistogramName:                 "{By}",
	TCPFirewallConnectionDurationHistogramName:    "ms",
	TCPFirewallConnectionsPerSandboxHistogramName: "{connection}",

	IngressProxyConnectionDurationHistogramName:    "ms",
	IngressProxyConnectionsPerSandboxHistogramName: "{connection}",

	// Firecracker net histograms
	SandboxFCNetBytes:                 "{By}",
	SandboxFCNetPackets:               "{packet}",
	SandboxFCNetCount:                 "{op}",
	SandboxFCNetRateLimiterThrottled:  "{op}",
	SandboxFCNetRateLimiterEventCount: "{event}",
	SandboxFCNetRemainingReqs:         "{event}",

	// Firecracker block histograms
	SandboxFCBlockBytes:                 "{By}",
	SandboxFCBlockCount:                 "{op}",
	SandboxFCBlockRateLimiterThrottled:  "{op}",
	SandboxFCBlockRateLimiterEventCount: "{event}",
	SandboxFCBlockIOEngineThrottled:     "{op}",
	SandboxFCBlockRemainingReqs:         "{event}",

	SnapshotDiffBytes:  "{By}",
	SnapshotDiffRatio:  "{1}",
	SnapshotTotalBytes: "{By}",

	UploadUncompressedBytes: "{By}",
	UploadCompressedBytes:   "{By}",
	UploadCompressionRatio:  "{1}",
}

func GetHistogram(meter metric.Meter, name HistogramType) (metric.Int64Histogram, error) {
	desc := histogramDesc[name]
	unit := histogramUnits[name]

	return meter.Int64Histogram(string(name),
		metric.WithDescription(desc),
		metric.WithUnit(unit),
	)
}

func GetFloatHistogram(meter metric.Meter, name HistogramType) (metric.Float64Histogram, error) {
	desc := histogramDesc[name]
	unit := histogramUnits[name]

	return meter.Float64Histogram(string(name),
		metric.WithDescription(desc),
		metric.WithUnit(unit),
	)
}

type TimerFactory struct {
	duration metric.Int64Histogram
	bytes    metric.Int64Counter
	count    metric.Int64Counter
}

func NewTimerFactory(
	blocksMeter metric.Meter,
	metricName, durationDescription, bytesDescription, counterDescription string,
) (TimerFactory, error) {
	duration, err := blocksMeter.Int64Histogram(metricName,
		metric.WithDescription(durationDescription),
		metric.WithUnit("ms"),
	)
	if err != nil {
		return TimerFactory{}, fmt.Errorf("failed to get slices metric: %w", err)
	}

	bytes, err := blocksMeter.Int64Counter(metricName,
		metric.WithDescription(bytesDescription),
		metric.WithUnit("By"),
	)
	if err != nil {
		return TimerFactory{}, fmt.Errorf("failed to create total bytes requested metric: %w", err)
	}

	count, err := blocksMeter.Int64Counter(metricName,
		metric.WithDescription(counterDescription),
	)
	if err != nil {
		return TimerFactory{}, fmt.Errorf("failed to create total page faults metric: %w", err)
	}

	return TimerFactory{duration, bytes, count}, nil
}

// FloatTimerFactory records duration as fractional milliseconds so sub-ms
// operations aren't truncated to 0. The duration histogram and event counter
// share <metricName> (rate()-friendly); only the bytes counter splits out to
// <metricName>.size so Grafana's unit detection doesn't conflate ms with By.
type FloatTimerFactory struct {
	duration metric.Float64Histogram
	bytes    metric.Int64Counter
	count    metric.Int64Counter
}

// SubMillisecondMsBuckets resolve sub-ms operations (mmap / cache hits) that the
// default OTEL buckets (first boundary 5ms) collapse into one, while still
// covering remote reads to ~10s.
var SubMillisecondMsBuckets = []float64{
	0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 25, 50, 100, 250, 500, 1000, 2500, 5000, 10000,
}

func NewFloatTimerFactory(
	meter metric.Meter,
	metricName, durationDescription, bytesDescription string,
) (FloatTimerFactory, error) {
	duration, err := meter.Float64Histogram(metricName,
		metric.WithDescription(durationDescription),
		metric.WithUnit("ms"),
		metric.WithExplicitBucketBoundaries(SubMillisecondMsBuckets...),
	)
	if err != nil {
		return FloatTimerFactory{}, fmt.Errorf("failed to create duration histogram: %w", err)
	}

	bytes, err := meter.Int64Counter(metricName+".size",
		metric.WithDescription(bytesDescription),
		metric.WithUnit("By"),
	)
	if err != nil {
		return FloatTimerFactory{}, fmt.Errorf("failed to create bytes counter: %w", err)
	}

	count, err := meter.Int64Counter(metricName,
		metric.WithDescription("Total "+metricName+" events recorded"),
	)
	if err != nil {
		return FloatTimerFactory{}, fmt.Errorf("failed to create count counter: %w", err)
	}

	return FloatTimerFactory{duration, bytes, count}, nil
}

func (f *FloatTimerFactory) Record(ctx context.Context, dur time.Duration, total int64, attrs metric.MeasurementOption) {
	f.duration.Record(ctx, float64(dur)/float64(time.Millisecond), attrs)
	f.bytes.Add(ctx, total, attrs)
	f.count.Add(ctx, 1, attrs)
}

func (f *TimerFactory) Begin(kv ...attribute.KeyValue) *Stopwatch {
	return &Stopwatch{
		histogram: f.duration,
		sum:       f.bytes,
		count:     f.count,
		start:     time.Now(),
		kv:        kv,
	}
}

type Stopwatch struct {
	histogram  metric.Int64Histogram
	sum, count metric.Int64Counter
	start      time.Time
	kv         []attribute.KeyValue
}

const (
	resultAttr        = "result"
	resultTypeSuccess = "success"
	resultTypeFailure = "failure"
)

var (
	// Pre-allocated result attributes for use with PrecomputeAttrs.
	Success = attribute.String(resultAttr, resultTypeSuccess)
	Failure = attribute.String(resultAttr, resultTypeFailure)
)

func (t Stopwatch) Success(ctx context.Context, total int64, kv ...attribute.KeyValue) {
	t.end(ctx, resultTypeSuccess, total, kv...)
}

func (t Stopwatch) Failure(ctx context.Context, total int64, kv ...attribute.KeyValue) {
	t.end(ctx, resultTypeFailure, total, kv...)
}

func (t Stopwatch) end(ctx context.Context, result string, total int64, kv ...attribute.KeyValue) {
	kv = append(kv, attribute.KeyValue{Key: resultAttr, Value: attribute.StringValue(result)})
	kv = append(t.kv, kv...)
	opt := metric.WithAttributeSet(attribute.NewSet(kv...))
	t.RecordRaw(ctx, total, opt)
}

// PrecomputeAttrs builds a reusable MeasurementOption from the given attribute
// key-values. The option must include all attributes (including "result").
// Use with Stopwatch.Record to avoid per-call attribute allocation.
func PrecomputeAttrs(kv ...attribute.KeyValue) metric.MeasurementOption {
	return metric.WithAttributeSet(attribute.NewSet(kv...))
}

// RecordRaw records an operation using a precomputed attribute option, it does
// not include any previous attributes passed at Begin(). Zero-allocation
// alternative to Success/Failure for hot paths.
func (t Stopwatch) RecordRaw(ctx context.Context, total int64, allAttrs metric.MeasurementOption) {
	amount := time.Since(t.start).Milliseconds()
	t.histogram.Record(ctx, amount, allAttrs)
	t.sum.Add(ctx, total, allAttrs)
	t.count.Add(ctx, 1, allAttrs)
}
