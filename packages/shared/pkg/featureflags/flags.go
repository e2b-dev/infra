package featureflags

import (
	"context"
	"fmt"
	"strings"

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

	OrchestratorKind            ldcontext.Kind = "orchestrator"
	OrchestratorCommitAttribute string         = "commit"
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

func newJSONFlag(name string, fallback ldvalue.Value) JSONFlag {
	flag := JSONFlag{name: name, fallback: fallback}
	builder := launchDarklyOfflineStore.Flag(flag.name).ValueForAll(fallback)
	launchDarklyOfflineStore.Update(builder)

	return flag
}

var CleanNFSCache = newJSONFlag("clean-nfs-cache", ldvalue.Null())

// RateLimitConfigFlag provides per-team rate limit overrides.
// JSON format:
//
//	{
//	  "/sandboxes/": {"rate": 50, "burst": 100},
//	  "/sandboxes/:sandboxID/pause": {"rate": 10, "burst": 20}
//	}
//
// When non-null, values override the code defaults. Target specific teams in LaunchDarkly.
var RateLimitConfigFlag = newJSONFlag("rate-limit-config", ldvalue.Null())

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

func newBoolFlag(name string, fallback bool) BoolFlag {
	flag := BoolFlag{name: name, fallback: fallback}
	builder := launchDarklyOfflineStore.Flag(flag.name).VariationForAll(fallback)
	launchDarklyOfflineStore.Update(builder)

	return flag
}

var (
	MetricsWriteFlag                    = newBoolFlag("sandbox-metrics-write", true)
	MetricsReadFlag                     = newBoolFlag("sandbox-metrics-read", true)
	SnapshotFeatureFlag                 = newBoolFlag("use-nfs-for-snapshots", env.IsDevelopment())
	TemplateFeatureFlag                 = newBoolFlag("use-nfs-for-templates", env.IsDevelopment())
	EnableWriteThroughCacheFlag         = newBoolFlag("write-to-cache-on-writes", false)
	UseNFSCacheForBuildingTemplatesFlag = newBoolFlag("use-nfs-for-building-templates", env.IsDevelopment())
	BestOfKCanFitFlag                   = newBoolFlag("best-of-k-can-fit", true)
	BestOfKTooManyStartingFlag          = newBoolFlag("best-of-k-too-many-starting", false)
	EdgeProvidedSandboxMetricsFlag      = newBoolFlag("edge-provided-sandbox-metrics", false)
	CreateStorageCacheSpansFlag         = newBoolFlag("create-storage-cache-spans", env.IsDevelopment())
	SandboxAutoResumeFlag               = newBoolFlag("sandbox-auto-resume", env.IsDevelopment())
	SandboxCatalogLocalCacheFlag        = newBoolFlag("sandbox-catalog-local-cache", true)
	PersistentVolumesFlag               = newBoolFlag("can-use-persistent-volumes", env.IsDevelopment())
	ExecutionMetricsOnWebhooksFlag      = newBoolFlag("execution-metrics-on-webhooks", false) // TODO: Remove NLT 20250315
	// PeerToPeerChunkTransferFlag enables peer-to-peer chunk routing.
	PeerToPeerChunkTransferFlag = newBoolFlag("peer-to-peer-chunk-transfer", false)
	// PeerToPeerAsyncCheckpointFlag makes Checkpoint upload fire-and-forget instead
	// of synchronous. Only safe to enable after PeerToPeerChunkTransferFlag is ON.
	PeerToPeerAsyncCheckpointFlag   = newBoolFlag("peer-to-peer-async-checkpoint", false)
	SandboxLabelBasedSchedulingFlag = newBoolFlag("sandbox-label-based-scheduling", false)
)

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

func newIntFlag(name string, fallback int) IntFlag {
	flag := IntFlag{name: name, fallback: fallback}
	builder := launchDarklyOfflineStore.Flag(flag.name).ValueForAll(ldvalue.Int(fallback))
	launchDarklyOfflineStore.Update(builder)

	return flag
}

var (
	MaxSandboxesPerNode           = newIntFlag("max-sandboxes-per-node", 200)
	GcloudConcurrentUploadLimit   = newIntFlag("gcloud-concurrent-upload-limit", 8)
	GcloudMaxTasks                = newIntFlag("gcloud-max-tasks", 16)
	ClickhouseBatcherMaxBatchSize = newIntFlag("clickhouse-batcher-max-batch-size", 100)
	ClickhouseBatcherMaxDelay     = newIntFlag("clickhouse-batcher-max-delay", 1000) // 1s in milliseconds
	ClickhouseBatcherQueueSize    = newIntFlag("clickhouse-batcher-queue-size", 1000)
	BestOfKSampleSize             = newIntFlag("best-of-k-sample-size", 3)                   // Default K=3
	BestOfKMaxOvercommit          = newIntFlag("best-of-k-max-overcommit", 400)              // Default R=4 (stored as percentage, max over-commit ratio)
	BestOfKAlpha                  = newIntFlag("best-of-k-alpha", 50)                        // Default Alpha=0.5 (stored as percentage for int flag, current usage weight)
	EnvdInitTimeoutMilliseconds   = newIntFlag("envd-init-request-timeout-milliseconds", 50) // Timeout for envd init request in milliseconds
	HostStatsSamplingInterval     = newIntFlag("host-stats-sampling-interval", 5000)         // Host stats sampling interval in milliseconds (default 5s)
	MaxCacheWriterConcurrencyFlag = newIntFlag("max-cache-writer-concurrency", 10)

	// BuildCacheMaxUsagePercentage the maximum percentage of the cache disk storage
	// that can be used before the cache starts evicting items.
	BuildCacheMaxUsagePercentage = newIntFlag("build-cache-max-usage-percentage", 85)
	BuildProvisionVersion        = newIntFlag("build-provision-version", 0)

	// NBDConnectionsPerDevice the number of NBD socket connections per device
	NBDConnectionsPerDevice = newIntFlag("nbd-connections-per-device", 4)

	// MemoryPrefetchMaxFetchWorkers is the maximum number of parallel fetch workers per sandbox for memory prefetching.
	// Fetching is I/O bound so we can have more parallelism.
	MemoryPrefetchMaxFetchWorkers = newIntFlag("memory-prefetch-max-fetch-workers", 16)

	// MemoryPrefetchMaxCopyWorkers is the maximum number of parallel copy workers per sandbox for memory prefetching.
	// Copy uses uffd syscalls, so we limit parallelism to avoid overwhelming the system.
	MemoryPrefetchMaxCopyWorkers = newIntFlag("memory-prefetch-max-copy-workers", 8)

	// TCPFirewallMaxConnectionsPerSandbox is the maximum number of concurrent TCP firewall
	// connections allowed per sandbox. Negative means no limit.
	TCPFirewallMaxConnectionsPerSandbox = newIntFlag("tcpfirewall-max-connections-per-sandbox", -1)

	// SandboxMaxIncomingConnections is the maximum number of concurrent HTTP proxy
	// connections allowed per sandbox. Negative means no limit.
	SandboxMaxIncomingConnections = newIntFlag("sandbox-max-incoming-connections", -1)

	// BuildBaseRootfsSizeLimitMB is the maximum size of the base rootfs filesystem created from the OCI image, in MB.
	BuildBaseRootfsSizeLimitMB = newIntFlag("build-base-rootfs-size-limit-mb", 25000)

	// MinAutoResumeTimeoutSeconds is the minimum auto-resume timeout in seconds.
	// This prevents thrashing from very short timeouts.
	MinAutoResumeTimeoutSeconds = newIntFlag("minimum-autoresume-timeout", 300)

	// BuildReservedDiskSpaceMB is the amount of disk space in MB reserved for root on the guest filesystem.
	// Reserved blocks are only usable by root (uid 0), protecting the guest OS from disk-full conditions.
	BuildReservedDiskSpaceMB = newIntFlag("build-reserved-disk-space-mb", 0)

	// MaxConcurrentSnapshotUpserts limits concurrent UpsertSnapshot calls (pause + snapshot template paths).
	// 0 or negative disables throttling (unlimited concurrency).
	MaxConcurrentSnapshotUpserts = newIntFlag("max-concurrent-snapshot-upserts", 0)
	// MaxConcurrentSandboxListQueries limits concurrent GetSnapshotsWithCursor calls in the sandbox list path.
	// 0 or negative disables throttling (unlimited concurrency).
	MaxConcurrentSandboxListQueries = newIntFlag("max-concurrent-sandbox-list-queries", 0)
	// MaxConcurrentSnapshotBuildQueries limits concurrent GetSnapshotBuilds calls (e.g. sandbox delete).
	// 0 or negative disables throttling (unlimited concurrency).
	MaxConcurrentSnapshotBuildQueries = newIntFlag("max-concurrent-snapshot-build-queries", 0)
)

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

func newStringFlag(name string, fallback string) StringFlag {
	flag := StringFlag{name: name, fallback: fallback}
	builder := launchDarklyOfflineStore.Flag(flag.name).ValueForAll(ldvalue.String(fallback))
	launchDarklyOfflineStore.Update(builder)

	return flag
}

// This is currently not configurable via feature flags.
const (
	DefaultKernelVersion = "vmlinux-6.1.158"
)

// The Firecracker version the last tag + the short SHA (so we can build our dev previews)
// TODO: The short tag here has only 7 characters — the one from our build pipeline will likely have exactly 8 so this will break.
const (
	DefaultFirecackerV1_10Version = "v1.10.1_30cbb07"
	DefaultFirecackerV1_12Version = "v1.12.1_a41d3fb"
	DefaultFirecrackerVersion     = DefaultFirecackerV1_12Version
)

var FirecrackerVersionMap = map[string]string{
	"v1.10": DefaultFirecackerV1_10Version,
	"v1.12": DefaultFirecackerV1_12Version,
}

// BuildIoEngine Sync is used by default as there seems to be a bad interaction between Async and a lot of io operations.
var (
	BuildFirecrackerVersion     = newStringFlag("build-firecracker-version", env.GetEnv("DEFAULT_FIRECRACKER_VERSION", DefaultFirecrackerVersion))
	BuildIoEngine               = newStringFlag("build-io-engine", "Sync")
	DefaultPersistentVolumeType = newStringFlag("default-persistent-volume-type", "")
	BuildNodeInfo               = newJSONFlag("preferred-build-node", ldvalue.Null())
	FirecrackerVersions         = newJSONFlag("firecracker-versions", ldvalue.FromJSONMarshal(FirecrackerVersionMap))
)

// ResolveFirecrackerVersion resolves the firecracker version using the FirecrackerVersions feature flag.
// The buildVersion format is "v1.12.1_a41d3fb" — we extract "v1.12" as the lookup key.
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
var TrackedTemplatesForMetrics = newJSONFlag("tracked-templates-for-metrics", ldvalue.FromJSONMarshal(defaultTrackedTemplates))

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

// OverrideJSONFlag updates a JSON flag value in the offline store.
// Intended for benchmarks and tests.
func OverrideJSONFlag(flag JSONFlag, value ldvalue.Value) {
	builder := launchDarklyOfflineStore.Flag(flag.Key()).ValueForAll(value)
	launchDarklyOfflineStore.Update(builder)
}

// CompressConfigFlag controls compression during template builds.
// When compressBuilds is true, builds upload exclusively compressed data
// (no uncompressed fallback). When false, exclusively uncompressed with V3 headers.
var CompressConfigFlag = newJSONFlag("compress-config", ldvalue.FromJSONMarshal(map[string]any{
	"compressBuilds":     false,
	"compressionType":    "zstd",
	"compressionLevel":   2,
	"frameSizeKB":        2048,
	"targetPartSizeMB":   50,
	"frameEncodeWorkers": 4,
	"encoderConcurrency": 1,
	"decoderConcurrency": 1,
}))

// TCPFirewallEgressThrottleConfig controls per-sandbox egress throttling via Firecracker's
// VMM-level token bucket rate limiters on the network interface.
// Structure mirrors the Firecracker RateLimiter API: two independent token buckets.
// Set bucketSize to -1 to disable a bucket.
//
// Ops bucket (packets):    effective rate = ops.bucketSize * 1000 / ops.refillTimeMs ops/s.
// Bandwidth bucket (bytes): effective rate = bandwidth.bucketSize * 1000 / bandwidth.refillTimeMs bytes/s.
var TCPFirewallEgressThrottleConfig = newJSONFlag("tcpfirewall-egress-throttle-config", ldvalue.FromJSONMarshal(map[string]any{
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

// GetTCPFirewallEgressThrottleConfig fetches and parses the TCPFirewallEgressThrottleConfig flag.
func GetTCPFirewallEgressThrottleConfig(ctx context.Context, ff *Client) TCPFirewallEgressThrottleConfigValue {
	value := ff.JSONFlag(ctx, TCPFirewallEgressThrottleConfig)

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

	return TCPFirewallEgressThrottleConfigValue{
		Ops:       parseBucket("ops"),
		Bandwidth: parseBucket("bandwidth"),
	}
}
