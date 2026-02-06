package feature_flags

import (
	"context"

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

	TeamKind       ldcontext.Kind = "team"
	UserKind       ldcontext.Kind = "user"
	ClusterKind    ldcontext.Kind = "cluster"
	deploymentKind ldcontext.Kind = "deployment"
	TierKind       ldcontext.Kind = "tier"
	ServiceKind    ldcontext.Kind = "service"
	TemplateKind   ldcontext.Kind = "template"
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
	MetricsWriteFlag                    = newBoolFlag("sandbox-metrics-write", env.IsDevelopment())
	MetricsReadFlag                     = newBoolFlag("sandbox-metrics-read", env.IsDevelopment())
	SnapshotFeatureFlag                 = newBoolFlag("use-nfs-for-snapshots", env.IsDevelopment())
	TemplateFeatureFlag                 = newBoolFlag("use-nfs-for-templates", env.IsDevelopment())
	EnableWriteThroughCacheFlag         = newBoolFlag("write-to-cache-on-writes", false)
	UseNFSCacheForBuildingTemplatesFlag = newBoolFlag("use-nfs-for-building-templates", env.IsDevelopment())
	BestOfKCanFitFlag                   = newBoolFlag("best-of-k-can-fit", true)
	BestOfKTooManyStartingFlag          = newBoolFlag("best-of-k-too-many-starting", false)
	EdgeProvidedSandboxMetricsFlag      = newBoolFlag("edge-provided-sandbox-metrics", false)
	CreateStorageCacheSpansFlag         = newBoolFlag("create-storage-cache-spans", env.IsDevelopment())
	SandboxAutoResumeFlag               = newBoolFlag("sandbox-auto-resume", env.IsDevelopment())
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

// The Firecracker version the last tag + the short SHA (so we can build our dev previews)
// TODO: The short tag here has only 7 characters — the one from our build pipeline will likely have exactly 8 so this will break.
const (
	DefaultFirecackerV1_10Version = "v1.10.1_fb257a1"
	DefaultFirecackerV1_12Version = "v1.12.1_717921c"
	DefaultFirecrackerVersion     = DefaultFirecackerV1_12Version
)

var firecrackerVersions = map[string]string{
	"v1.10": DefaultFirecackerV1_10Version,
	"v1.12": DefaultFirecackerV1_12Version,
}

// BuildIoEngine Sync is used by default as there seems to be a bad interaction between Async and a lot of io operations.
var (
	BuildFirecrackerVersion = newStringFlag("build-firecracker-version", env.GetEnv("DEFAULT_FIRECRACKER_VERSION", DefaultFirecrackerVersion))
	BuildIoEngine           = newStringFlag("build-io-engine", "Sync")
	BuildNodeInfo           = newJSONFlag("preferred-build-node", ldvalue.Null())
	FirecrackerVersions     = newJSONFlag("firecracker-versions", ldvalue.FromJSONMarshal(firecrackerVersions))
)

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
