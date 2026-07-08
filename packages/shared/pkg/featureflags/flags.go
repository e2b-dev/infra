package featureflags

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/launchdarkly/go-sdk-common/v3/ldcontext"
	"github.com/launchdarkly/go-sdk-common/v3/ldvalue"

	"github.com/e2b-dev/infra/packages/shared/pkg/env"
)

// kinds
const (
	SandboxKind                        ldcontext.Kind = "sandbox"
	SandboxTemplateAttribute           string         = "template-id"
	SandboxKernelVersionAttribute      string         = "kernel-version"
	SandboxFirecrackerVersionAttribute string         = "firecracker-version"
	SandboxEnvdVersionAttribute        string         = "envd-version"
	// SandboxTypeAttribute distinguishes "sandbox" from "build" runs.
	SandboxTypeAttribute string = "sandbox-type"

	TeamKind             ldcontext.Kind = "team"
	UserKind             ldcontext.Kind = "user"
	ClusterKind          ldcontext.Kind = "cluster"
	deploymentKind       ldcontext.Kind = "deployment"
	TierKind             ldcontext.Kind = "tier"
	ServiceKind          ldcontext.Kind = "service"
	TemplateKind         ldcontext.Kind = "template"
	VolumeKind           ldcontext.Kind = "volume"
	CompressFileTypeKind ldcontext.Kind = "compress-file-type"
	CompressUseCaseKind  ldcontext.Kind = "compress-use-case"
)

// All flags must be defined here: https://app.launchdarkly.com/projects/default/flags/

type JSONFlag struct {
	name     string
	fallback ldvalue.Value
}

func (f JSONFlag) Key() string {
	return f.name
}

func (f JSONFlag) String() string {
	return f.name
}

func (f JSONFlag) Fallback() ldvalue.Value {
	return f.fallback
}

func NewJSONFlag(name string, fallback ldvalue.Value) JSONFlag {
	flag := JSONFlag{name: name, fallback: fallback}
	builder := launchDarklyOfflineStore.Flag(flag.name).ValueForAll(fallback)
	launchDarklyOfflineStore.Update(builder)

	return flag
}

var CleanNFSCache = NewJSONFlag("clean-nfs-cache", ldvalue.Null())

// RateLimitConfigFlag provides per-team rate limit overrides.
// JSON format:
//
//	{
//	  "/sandboxes/": {"rate": 50, "burst": 100},
//	  "/sandboxes/:sandboxID/pause": {"rate": 10, "burst": 20}
//	}
//
// When non-null, values override the code defaults. Target specific teams in LaunchDarkly.
var RateLimitConfigFlag = NewJSONFlag("rate-limit-config", ldvalue.Null())

type BoolFlag struct {
	name     string
	fallback bool
}

func (f BoolFlag) Key() string {
	return f.name
}

func (f BoolFlag) String() string {
	return f.name
}

func (f BoolFlag) Fallback() bool {
	return f.fallback
}

func NewBoolFlag(name string, fallback bool) BoolFlag {
	flag := BoolFlag{name: name, fallback: fallback}
	builder := launchDarklyOfflineStore.Flag(flag.name).VariationForAll(fallback)
	launchDarklyOfflineStore.Update(builder)

	return flag
}

// OverrideBoolFlag forces a bool flag to a specific value in the offline store.
// Only takes effect when LAUNCH_DARKLY_API_KEY is not set (i.e. dev/CLI tools).
func OverrideBoolFlag(flag BoolFlag, value bool) {
	builder := launchDarklyOfflineStore.Flag(flag.name).VariationForAll(value)
	launchDarklyOfflineStore.Update(builder)
}

// OverrideJSONFlag forces a JSON flag to a specific value in the offline store.
// Only takes effect when LAUNCH_DARKLY_API_KEY is not set (i.e. dev/CLI tools).
func OverrideJSONFlag(flag JSONFlag, value ldvalue.Value) {
	builder := launchDarklyOfflineStore.Flag(flag.name).ValueForAll(value)
	launchDarklyOfflineStore.Update(builder)
}

var (
	SnapshotFeatureFlag                 = NewBoolFlag("use-nfs-for-snapshots", env.IsDevelopment())
	TemplateFeatureFlag                 = NewBoolFlag("use-nfs-for-templates", env.IsDevelopment())
	EnableWriteThroughCacheFlag         = NewBoolFlag("write-to-cache-on-writes", false)
	UseNFSCacheForBuildingTemplatesFlag = NewBoolFlag("use-nfs-for-building-templates", env.IsDevelopment())
	CreateStorageCacheSpansFlag         = NewBoolFlag("create-storage-cache-spans", env.IsDevelopment())
	OrchAcceptsCombinedHostFlag         = NewBoolFlag("orch-accepts-combined-host", false)

	// StorageSoftDeleteCheckFlag enables reading the storage-index soft-delete
	// tombstone on header load (one extra GCS Attrs on cold load). Off = no overhead.
	StorageSoftDeleteCheckFlag = NewBoolFlag("storage-soft-delete-check", false)
	// StorageSoftDeleteEnforceFlag makes a soft-deleted object fail the read
	// (fail closed) instead of only emitting a metric + log. Requires the check flag.
	StorageSoftDeleteEnforceFlag = NewBoolFlag("storage-soft-delete-enforce", false)

	// UseMemFdFlag asks Firecracker to back guest memory with a memfd and
	// pass the fd over the UFFD socket; the orchestrator then mmaps it
	// directly instead of using process_vm_readv on pause.
	UseMemFdFlag = NewBoolFlag("use-memfd", true)

	// MemfdBackgroundCopyFlag streams the memfd into the snapshot cache on
	// a goroutine so Pause returns as soon as the diff metadata is written.
	// Only takes effect when UseMemFdFlag is also on.
	MemfdBackgroundCopyFlag = NewBoolFlag("memfd-background-copy", true)

	// MemfileDiffDedupFlag enables 4 KiB-page dedup of the memfile diff
	// against the base memfile. bestEffort skips uncached blocks; directIO
	// opens the dedup output with O_DIRECT. The remaining keys budget fetch
	// defragmentation of the deduped diff — fetchRunWindowPages is the
	// uncompressed frame/window size served per backing fetch — see
	// orchestrator block.DedupBudget for semantics (0 = disabled/default).
	MemfileDiffDedupFlag = NewJSONFlag("memfile-diff-dedup", ldvalue.FromJSONMarshal(map[string]any{
		"enabled":                        false,
		"bestEffort":                     false,
		"directIO":                       false,
		"maxFetchWindowsPerBlock":        0,
		"maxPromotedParentPagesPerBlock": 0,
		"maxPagesPerPromotedFrame":       0,
		"blockFaultPct":                  0,
		"fetchRunWindowPages":            0,
	}))

	// PeerToPeerChunkTransferFlag enables peer-to-peer chunk routing.
	PeerToPeerChunkTransferFlag = NewBoolFlag("peer-to-peer-chunk-transfer", false)
	// PeerToPeerAsyncCheckpointFlag makes Checkpoint upload fire-and-forget instead
	// of synchronous. Only safe to enable after PeerToPeerChunkTransferFlag is ON.
	PeerToPeerAsyncCheckpointFlag = NewBoolFlag("peer-to-peer-async-checkpoint", false)

	PersistentVolumesFlag            = NewBoolFlag("can-use-persistent-volumes", env.IsDevelopment())
	SandboxLabelBasedSchedulingFlag  = NewBoolFlag("sandbox-label-based-scheduling", false)
	OptimisticResourceAccountingFlag = NewBoolFlag("sandbox-placement-optimistic-resource-accounting", false)
	FreePageReportingFlag            = NewBoolFlag("free-page-reporting", false)
	FreezeUserCgroupFlag             = NewBoolFlag("freeze-user-cgroup", env.IsDevelopment())
	// CollapseEnvdHeapFlag makes the orchestrator ask envd to collapse its own
	// anonymous heap into 2 MiB hugepages just before pause, reducing the number
	// of distinct frames envd faults on resume. Off by default; rolled out via LD.
	CollapseEnvdHeapFlag = NewBoolFlag("collapse-envd-heap", false)

	// CollapseEnvdHeapTimeoutMsFlag bounds the pre-pause POST /collapse call, in
	// milliseconds. Collapsing migrates envd's scattered heap pages into
	// hugepages, which is heavier than the freeze sysfs write, so it gets a
	// larger, independent budget. Collapse is best-effort: a cut-short run still
	// helps, so this can be tuned per rollout without redeploying. The fallback
	// (returned when LD is unavailable or the flag is unset) is the default.
	CollapseEnvdHeapTimeoutMsFlag = NewIntFlag("collapse-envd-heap-timeout-ms", 10000) // 10s in milliseconds

	// VolumeFallbackToUnmatchedNodesFlag allows volume operations to fall back to
	// orchestrator nodes that don't advertise the volume's type label when every
	// labeled node fails with a retryable error. This is a transitional flag for
	// the volume-label migration: once every node is labeled, unlabeled nodes will
	// fail 100% of the time, so this should be turned off and removed afterwards.
	VolumeFallbackToUnmatchedNodesFlag = NewBoolFlag("volume-fallback-to-unmatched-nodes", true)

	// SandboxVolumeLabelBasedSchedulingFlag enables filtering orchestrator nodes
	// based on the volume types required by the sandbox. When enabled, labels
	// like "persistent-volume-type=nfs" are added to the required node labels
	// for sandbox placement.
	SandboxVolumeLabelBasedSchedulingFlag = NewBoolFlag("sandbox-volume-label-based-scheduling", false)

	NetworkTransformRulesFlag = NewBoolFlag("network-transform-rules", env.IsDevelopment())

	BYOPProxyEnabledFlag = NewBoolFlag("byop-proxy-enabled", env.IsDevelopment())

	// V4HeaderForUncompressedFlag forces the V4 header layout on uncompressed
	// uploads. Independent of compress-config: it changes the header format,
	// not whether data is compressed.
	V4HeaderForUncompressedFlag = NewBoolFlag("v4-header-for-uncompressed", false)

	// HeaderV5WriteFlag makes Pause emit V5 headers. When enabled it also
	// supersedes V4HeaderForUncompressedFlag for uncompressed uploads.
	HeaderV5WriteFlag = NewBoolFlag("header-v5-write", false)

	// ResumeOriginNodeRemapFlag enables repointing a snapshot's origin_node_id to
	// the fallback node a resume timed out on. The node's local cache is warming
	// from the in-progress snapshot pull, so pinning the retry to it avoids
	// re-pulling the snapshot onto yet another node.
	ResumeOriginNodeRemapFlag = NewBoolFlag("resume-origin-node-remap", false)

	// ExpirationIndexHealerFlag enables the API's Redis expiration index healer
	// loop, which re-adds sandboxes missing from the global expiration ZSET
	// (a missing member is never seen by the evictor and would live forever).
	// Checked on every heal tick, so it can be toggled without a redeploy.
	// On by default; acts as a kill switch if a heal pass misbehaves.
	ExpirationIndexHealerFlag = NewBoolFlag("expiration-index-healer", true)

	// DisableE2BAccessTokenProvisioningFlag stops POST /access-tokens from issuing
	// new E2B access tokens (sk_e2b_) once enabled. E2B_ACCESS_TOKEN is deprecated
	// in favor of E2B_API_KEY; the CLI now authenticates via Hydra JWTs. Off by
	// default so issuance keeps working until the deprecation cutover.
	DisableE2BAccessTokenProvisioningFlag = NewBoolFlag("disable-e2b-access-token-provisioning", false)
)

// envdTimeoutFallbackMs reads ENVD_TIMEOUT (Go duration string, e.g. "10s")
// and returns milliseconds. Falls back to 10 000 ms when unset or unparseable.
func envdTimeoutFallbackMs() int {
	raw := os.Getenv("ENVD_TIMEOUT")
	if raw == "" {
		return 10_000
	}

	d, err := time.ParseDuration(raw)
	if err != nil {
		return 10_000
	}

	return int(d.Milliseconds())
}

type IntFlag struct {
	name     string
	fallback int
}

func (f IntFlag) Key() string {
	return f.name
}

func (f IntFlag) String() string {
	return f.name
}

func (f IntFlag) Fallback() int {
	return f.fallback
}

func NewIntFlag(name string, fallback int) IntFlag {
	flag := IntFlag{name: name, fallback: fallback}
	builder := launchDarklyOfflineStore.Flag(flag.name).ValueForAll(ldvalue.Int(fallback))
	launchDarklyOfflineStore.Update(builder)

	return flag
}

var (
	MaxSandboxesPerNode = NewIntFlag("max-sandboxes-per-node", 200)
	// The LD keys keep the legacy "gcloud-" prefix, but the limits apply to uploads on all storage providers.
	StorageConcurrentUploadLimit  = NewIntFlag("gcloud-concurrent-upload-limit", 8)
	StorageMaxUploadTasks         = NewIntFlag("gcloud-max-tasks", 16)
	ClickhouseBatcherMaxBatchSize = NewIntFlag("clickhouse-batcher-max-batch-size", 100)
	ClickhouseBatcherMaxDelay     = NewIntFlag("clickhouse-batcher-max-delay", 1000) // 1s in milliseconds
	ClickhouseBatcherQueueSize    = NewIntFlag("clickhouse-batcher-queue-size", 1000)
	BestOfKSampleSize             = NewIntFlag("best-of-k-sample-size", 3)                           // Default K=3
	BestOfKMaxOvercommit          = NewIntFlag("best-of-k-max-overcommit", 400)                      // Default R=4 (stored as percentage, max over-commit ratio)
	BestOfKAlpha                  = NewIntFlag("best-of-k-alpha", 50)                                // Default Alpha=0.5 (stored as percentage for int flag, current usage weight)
	EnvdInitTimeoutMilliseconds   = NewIntFlag("envd-init-request-timeout-milliseconds", 50)         // Timeout for envd init request in milliseconds
	EnvdTimeoutMilliseconds       = NewIntFlag("envd-timeout-milliseconds", envdTimeoutFallbackMs()) // Timeout for waiting for envd on resume; falls back to ENVD_TIMEOUT env var (default 10s)
	// GuestSyncTimeoutMs overrides the mandatory pre-pause guest-sync deadline
	// for filesystem-only snapshots, in milliseconds. 0 (default) derives the
	// timeout from guest RAM; a positive value pins it.
	GuestSyncTimeoutMs            = NewIntFlag("guest-sync-timeout-milliseconds", 0)
	MaxCacheWriterConcurrencyFlag = NewIntFlag("max-cache-writer-concurrency", 10)

	// BuildCacheMaxUsagePercentage the maximum percentage of the cache disk storage
	// that can be used before the cache starts evicting items.
	BuildCacheMaxUsagePercentage = NewIntFlag("build-cache-max-usage-percentage", 85)
	BuildProvisionVersion        = NewIntFlag("build-provision-version", 0)

	// NBDConnectionsPerDevice the number of NBD socket connections per device
	NBDConnectionsPerDevice = NewIntFlag("nbd-connections-per-device", 1)

	// NBDAsyncWriteZeroesFlag, when enabled, handles NBD WRITE_ZEROES/TRIM
	// commands in a goroutine instead of inline on the dispatch read loop.
	// Inline handling can stall the read loop via head-of-line blocking on the
	// shared write lock (when a reply writer is blocked on a full socket send
	// buffer), which makes the kernel time out the NBD connection and surfaces
	// as guest I/O errors. Disabled by default.
	NBDAsyncWriteZeroesFlag = NewBoolFlag("nbd-async-write-zeroes", false)

	// MemoryPrefetchMaxFetchWorkers is the maximum number of parallel fetch workers per sandbox for memory prefetching.
	// Fetching is I/O bound so we can have more parallelism.
	MemoryPrefetchMaxFetchWorkers = NewIntFlag("memory-prefetch-max-fetch-workers", 16)

	// MemoryPrefetchMaxCopyWorkers is the maximum number of parallel copy workers per sandbox for memory prefetching.
	// Copy uses uffd syscalls, so we limit parallelism to avoid overwhelming the system.
	MemoryPrefetchMaxCopyWorkers = NewIntFlag("memory-prefetch-max-copy-workers", 8)

	// PauseResumePrefetchHarvestFlag makes the orchestrator, after a pause
	// snapshot is durable, run a throwaway warm resume of the just-written
	// artifact (driven by envd /init, workload frozen, egress denied) to record
	// the resume page-fault trace and turn it into a prefetch mapping. Off by
	// default; the harvest is best-effort and never affects the pause result.
	PauseResumePrefetchHarvestFlag = NewBoolFlag("pause-resume-prefetch-harvest", false)

	// PauseResumePrefetchConsumeFlag controls whether a harvested mapping is
	// persisted into the pause artifact metadata (and therefore replayed on the
	// customer's next resume). When off, the harvest still runs and emits its
	// trace-size metrics but does NOT write the mapping, so resumes are
	// unaffected — letting us validate harvest behaviour with no customer-visible
	// change before enabling prefetch on resume. Off by default.
	PauseResumePrefetchConsumeFlag = NewBoolFlag("pause-resume-prefetch-consume", false)

	// PauseResumePrefetchHarvestTimeoutMsFlag bounds the throwaway harvest resume
	// (slot-hold cap), in milliseconds. The harvest is best-effort: a cut-short
	// run is discarded (the build is simply re-harvested on its next pause), so
	// erring short is cheap. A normal warm harvest completes in a few seconds; the
	// default leaves headroom for a large warm resume to fully drain while keeping
	// the worst-case slot hold modest. Tunable per rollout via LD; the fallback
	// (returned when LD is unavailable or the flag is unset) is the default.
	PauseResumePrefetchHarvestTimeoutMsFlag = NewIntFlag("pause-resume-prefetch-harvest-timeout-ms", 15000) // 15s

	// TCPFirewallMaxConnectionsPerSandbox is the maximum number of concurrent TCP firewall
	// connections allowed per sandbox. Negative means no limit.
	TCPFirewallMaxConnectionsPerSandbox = NewIntFlag("tcpfirewall-max-connections-per-sandbox", -1)

	// SandboxMaxIncomingConnections is the maximum number of concurrent HTTP proxy
	// connections allowed per sandbox. Negative means no limit.
	SandboxMaxIncomingConnections = NewIntFlag("sandbox-max-incoming-connections", -1)

	// BuildBaseRootfsSizeLimitMB is the maximum size of the base rootfs filesystem created from the OCI image, in MB.
	BuildBaseRootfsSizeLimitMB = NewIntFlag("build-base-rootfs-size-limit-mb", 25000)

	// MinAutoResumeTimeoutSeconds is the minimum auto-resume timeout in seconds.
	// This prevents thrashing from very short timeouts.
	MinAutoResumeTimeoutSeconds = NewIntFlag("minimum-autoresume-timeout", 300)

	// BuildReservedDiskSpaceMB is the amount of disk space in MB reserved for root on the guest filesystem.
	// Reserved blocks are only usable by root (uid 0), protecting the guest OS from disk-full conditions.
	BuildReservedDiskSpaceMB = NewIntFlag("build-reserved-disk-space-mb", 256)

	// MaxStartingInstancesPerNode limits concurrent sandbox start/resume operations on a single orchestrator node.
	// Must be > 0.
	MaxStartingInstancesPerNode = NewIntFlag("max-starting-instances-per-node", 3)

	// MaxConcurrentEvictions caps the number of sandbox evictions that can run
	// in parallel per API instance. Excess items remain expired in the store
	// and are picked up by the next eviction tick. Must be > 0; non-positive
	// values are ignored at refresh time.
	MaxConcurrentEvictions = NewIntFlag("max-concurrent-evictions", 256)

	// MaxConcurrentSnapshotUpserts limits concurrent UpsertSnapshot calls (pause + snapshot template paths).
	// 0 or negative disables throttling (unlimited concurrency).
	MaxConcurrentSnapshotUpserts = NewIntFlag("max-concurrent-snapshot-upserts", 0)
	// MaxConcurrentSandboxListQueries limits concurrent GetSnapshotsWithCursor calls in the sandbox list path.
	// 0 or negative disables throttling (unlimited concurrency).
	MaxConcurrentSandboxListQueries = NewIntFlag("max-concurrent-sandbox-list-queries", 0)
	// MaxConcurrentSnapshotBuildQueries limits concurrent GetSnapshotBuilds calls (e.g. sandbox delete).
	// 0 or negative disables throttling (unlimited concurrency).
	MaxConcurrentSnapshotBuildQueries = NewIntFlag("max-concurrent-snapshot-build-queries", 0)

	MinChunkerReadSizeKB = NewIntFlag("min-chunker-read-size-kb", 16)

	// MaxParallelBuildReadSegments limits concurrent backing reads within one fragmented build read.
	// 1 or lower keeps the existing serial path.
	MaxParallelBuildReadSegments = NewIntFlag("max-parallel-build-read-segments", 1)
)

// ReclaimConfigFlag holds per-step caps in milliseconds for the pre-pause
// reclaim chain. Missing/zero/negative values disable the step.
// Example: {"sync":500,"drop_caches":200,"compact_memory":1000,"fstrim":500}
var ReclaimConfigFlag = NewJSONFlag("guest-pause-reclaim", ldvalue.Null())

// FreePageHintingConfig controls virtio-balloon free-page-hinting.
// "enabled" configures FreePageHinting=true on the balloon at install time
// (kernel-side eligibility is targeted separately via the LD context — the
// race fixed in https://lore.kernel.org/lkml/20240429125100.7393-1-david@redhat.com/
// is on the hinting flow, gated by the per-use-case timeouts below).
// "pause"/"build" are pre-pause drain timeouts in ms keyed by SnapshotUseCase;
// missing/zero/negative disables the drain for that use case.
// Example: {"enabled": true, "pause": 500, "build": 0}
var FreePageHintingConfig = NewJSONFlag("free-page-hinting-config", ldvalue.Null())

// IsFreePageHintingEnabled reports whether FPH should be configured on the
// balloon at install time.
func IsFreePageHintingEnabled(ctx context.Context, ff *Client, contexts ...ldcontext.Context) bool {
	return ff.JSONFlag(ctx, FreePageHintingConfig, contexts...).GetByKey("enabled").BoolValue()
}

// GetFreePageHintingTimeout returns the pre-pause FPH drain timeout for the
// given SnapshotUseCase. Zero means disabled.
func GetFreePageHintingTimeout(ctx context.Context, ff *Client, useCase string, contexts ...ldcontext.Context) time.Duration {
	ms := ff.JSONFlag(ctx, FreePageHintingConfig, contexts...).GetByKey(useCase).IntValue()
	if ms <= 0 {
		return 0
	}

	return time.Duration(ms) * time.Millisecond
}

type ReclaimConfig struct {
	Sync          time.Duration
	DropCaches    time.Duration
	CompactMemory time.Duration
	Fstrim        time.Duration
}

func GetReclaimConfig(ctx context.Context, ff *Client, contexts ...ldcontext.Context) ReclaimConfig {
	v := ff.JSONFlag(ctx, ReclaimConfigFlag, contexts...)
	ms := func(key string) time.Duration {
		return time.Duration(v.GetByKey(key).IntValue()) * time.Millisecond
	}

	return ReclaimConfig{
		Sync:          ms("sync"),
		DropCaches:    ms("drop_caches"),
		CompactMemory: ms("compact_memory"),
		Fstrim:        ms("fstrim"),
	}
}

type StringFlag struct {
	name     string
	fallback string
}

func (f StringFlag) Key() string {
	return f.name
}

func (f StringFlag) String() string {
	return f.name
}

func (f StringFlag) Fallback() string {
	return f.fallback
}

func NewStringFlag(name string, fallback string) StringFlag {
	flag := StringFlag{name: name, fallback: fallback}
	builder := launchDarklyOfflineStore.Flag(flag.name).ValueForAll(ldvalue.String(fallback))
	launchDarklyOfflineStore.Update(builder)

	return flag
}

const (
	DefaultKernelVersion = "vmlinux-6.1.158"
)

// The Firecracker version the last tag + the short SHA (so we can build our dev previews)
// TODO: The short tag here has only 7 characters — the one from our build pipeline will likely have exactly 8 so this will break.
const (
	DefaultFirecrackerV1_10Version = "v1.10.1_30cbb07"
	DefaultFirecrackerV1_12Version = "v1.12.1_210cbac"
	DefaultFirecrackerV1_14Version = "v1.14.1_431f1fc"
	DefaultFirecrackerVersion      = DefaultFirecrackerV1_14Version
)

var FirecrackerVersionMap = map[string]string{
	"v1.10": DefaultFirecrackerV1_10Version,
	"v1.12": DefaultFirecrackerV1_12Version,
	"v1.14": DefaultFirecrackerV1_14Version,
}

// BuildIoEngine Sync is used by default as there seems to be a bad interaction between Async and a lot of io operations.
var (
	BuildFirecrackerVersion     = NewStringFlag("build-firecracker-version", env.GetEnv("DEFAULT_FIRECRACKER_VERSION", DefaultFirecrackerVersion))
	BuildKernelVersion          = NewStringFlag("build-kernel-version", env.GetEnv("DEFAULT_KERNEL_VERSION", DefaultKernelVersion))
	BuildIoEngine               = NewStringFlag("build-io-engine", "Sync")
	DefaultPersistentVolumeType = NewStringFlag("default-persistent-volume-type", "")
	BuildNodeInfo               = NewJSONFlag("preferred-build-node", ldvalue.Null())
	FirecrackerVersions         = NewJSONFlag("firecracker-versions", ldvalue.FromJSONMarshal(FirecrackerVersionMap))

	// ClickhouseReadEndpointFlag selects which ClickHouse DSN to use for reads.
	// "" (empty) → singular CLICKHOUSE_CONNECTION_STRING (self-managed default).
	// "0", "1", ... → index into CLICKHOUSE_CONNECTION_STRINGS
	ClickhouseReadEndpointFlag = NewStringFlag("clickhouse-read-endpoint", "")

	// ClickhouseWriteFanoutFlag: when false, drop writes to alternate
	// ClickHouse endpoints (CLICKHOUSE_CONNECTION_STRINGS). Default DSN
	// is unaffected.
	ClickhouseWriteFanoutFlag = NewBoolFlag("clickhouse-write-fanout", false)
)

// ResolveFirecrackerVersion resolves the firecracker version using the FirecrackerVersions feature flag.
// The buildVersion format is "v1.12.1_210cbac" — we extract "v1.12" as the lookup key.
func ResolveFirecrackerVersion(ctx context.Context, ff *Client, buildVersion string) string {
	parts := strings.Split(buildVersion, "_")
	if len(parts) < 2 {
		return buildVersion
	}

	versionParts := strings.Split(strings.TrimPrefix(parts[0], "v"), ".")
	if len(versionParts) < 2 {
		return buildVersion
	}

	key := fmt.Sprintf("v%s.%s", versionParts[0], versionParts[1])
	versions := ff.JSONFlag(ctx, FirecrackerVersions).AsValueMap()

	if resolved, ok := versions.Get(key).AsOptionalString().Get(); ok {
		return resolved
	}

	return buildVersion
}

// defaultTrackedTemplates is the default map of template aliases tracked for metrics.
// This is used to reduce metric cardinality.
// JSON format: {"base": true, "code-interpreter-v1": true, ...}
var defaultTrackedTemplates = map[string]bool{
	"base":                  true,
	"code-interpreter-v1":   true,
	"code-interpreter-beta": true,
	"desktop":               true,
}

// TrackedTemplatesForMetrics is a JSON flag that defines which template aliases
// should be tracked in sandbox start time metrics. Templates not in this list
// will be grouped under "other" to reduce metric cardinality.
// JSON format: {"base": true, "code-interpreter-v1": true, ...}
var TrackedTemplatesForMetrics = NewJSONFlag("tracked-templates-for-metrics", ldvalue.FromJSONMarshal(defaultTrackedTemplates))

// GetTrackedTemplatesSet fetches the TrackedTemplatesForMetrics flag and returns it as a set for efficient lookup.
// Only keys with a truthy value are included; keys set to false are ignored.
func GetTrackedTemplatesSet(ctx context.Context, ff *Client) map[string]struct{} {
	value := ff.JSONFlag(ctx, TrackedTemplatesForMetrics)
	valueMap := value.AsValueMap()
	keys := valueMap.Keys(nil)
	result := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		if valueMap.Get(key).BoolValue() {
			result[key] = struct{}{}
		}
	}

	return result
}

// CompressConfigFlag controls compression during template builds.
// When compressBuilds is true, builds upload exclusively compressed data
// (no uncompressed fallback). When false, exclusively uncompressed with V3
// headers (unless V4HeaderForUncompressedFlag is set).
var CompressConfigFlag = NewJSONFlag("compress-config", ldvalue.FromJSONMarshal(map[string]any{
	"compressBuilds":     false,
	"compressionType":    "",
	"compressionLevel":   0,
	"frameSizeKB":        0,
	"minPartSizeMB":      0,
	"frameEncodeWorkers": 0,
	"encoderConcurrency": 0,
}))

// TCPFirewallEgressThrottleConfig controls per-sandbox egress throttling via Firecracker's
// VMM-level token bucket rate limiters on the network interface.
// Structure mirrors the Firecracker RateLimiter API: two independent token buckets.
// Set bucketSize to -1 to disable a bucket.
//
// Ops bucket (packets):    effective rate = ops.bucketSize * 1000 / ops.refillTimeMs ops/s.
// Bandwidth bucket (bytes): effective rate = bandwidth.bucketSize * 1000 / bandwidth.refillTimeMs bytes/s.
var TCPFirewallEgressThrottleConfig = NewJSONFlag("tcpfirewall-egress-throttle-config", ldvalue.FromJSONMarshal(map[string]any{
	"ops":       map[string]any{"bucketSize": -1, "oneTimeBurst": 0, "refillTimeMs": 1000},
	"bandwidth": map[string]any{"bucketSize": -1, "oneTimeBurst": 0, "refillTimeMs": 1000},
}))

// TokenBucketConfig holds parameters for a single Firecracker token bucket.
// BucketSize < 0 disables the bucket.
type TokenBucketConfig struct {
	BucketSize   int64
	OneTimeBurst int64
	RefillTimeMs int64
}

// TCPFirewallEgressThrottleConfigValue holds the parsed values of TCPFirewallEgressThrottleConfig.
type TCPFirewallEgressThrottleConfigValue struct {
	Ops       TokenBucketConfig
	Bandwidth TokenBucketConfig
}

// parseThrottleBuckets parses "ops" and "bandwidth" token bucket configs from a JSON flag value.
func parseThrottleBuckets(value ldvalue.Value) (ops, bandwidth TokenBucketConfig) {
	parseBucket := func(key string) TokenBucketConfig {
		b := value.GetByKey(key)
		if b.IsNull() {
			return TokenBucketConfig{BucketSize: -1} // disabled
		}

		// Validate refill time
		refillTimeMs := int64(b.GetByKey("refillTimeMs").IntValue())
		if refillTimeMs <= 0 {
			return TokenBucketConfig{BucketSize: -1} // disabled — invalid refill time
		}

		return TokenBucketConfig{
			BucketSize:   int64(b.GetByKey("bucketSize").IntValue()),
			OneTimeBurst: int64(b.GetByKey("oneTimeBurst").IntValue()),
			RefillTimeMs: refillTimeMs,
		}
	}

	return parseBucket("ops"), parseBucket("bandwidth")
}

// GetTCPFirewallEgressThrottleConfig fetches and parses the TCPFirewallEgressThrottleConfig flag.
func GetTCPFirewallEgressThrottleConfig(ctx context.Context, ff *Client) TCPFirewallEgressThrottleConfigValue {
	value := ff.JSONFlag(ctx, TCPFirewallEgressThrottleConfig)
	ops, bw := parseThrottleBuckets(value)

	return TCPFirewallEgressThrottleConfigValue{
		Ops:       ops,
		Bandwidth: bw,
	}
}

// BlockDriveThrottleConfig controls per-sandbox block device (disk) throttling via Firecracker's
// VMM-level token bucket rate limiters on the rootfs drive.
// Structure mirrors the Firecracker RateLimiter API: two independent token buckets.
// Set bucketSize to -1 to disable a bucket.
//
// Ops bucket (IOPS):       effective rate = ops.bucketSize * 1000 / ops.refillTimeMs ops/s.
// Bandwidth bucket (bytes): effective rate = bandwidth.bucketSize * 1000 / bandwidth.refillTimeMs bytes/s.
var BlockDriveThrottleConfig = NewJSONFlag("block-drive-throttle-config", ldvalue.FromJSONMarshal(map[string]any{
	"ops":       map[string]any{"bucketSize": -1, "oneTimeBurst": 0, "refillTimeMs": 1000},
	"bandwidth": map[string]any{"bucketSize": -1, "oneTimeBurst": 0, "refillTimeMs": 1000},
}))

// BlockDriveThrottleConfigValue holds the parsed values of BlockDriveThrottleConfig.
type BlockDriveThrottleConfigValue struct {
	Ops       TokenBucketConfig
	Bandwidth TokenBucketConfig
}

// GetBlockDriveThrottleConfig fetches and parses the BlockDriveThrottleConfig flag.
func GetBlockDriveThrottleConfig(ctx context.Context, ff *Client) BlockDriveThrottleConfigValue {
	value := ff.JSONFlag(ctx, BlockDriveThrottleConfig)
	ops, bw := parseThrottleBuckets(value)

	return BlockDriveThrottleConfigValue{
		Ops:       ops,
		Bandwidth: bw,
	}
}
