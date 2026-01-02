package feature_flags

import (
	"github.com/launchdarkly/go-sdk-common/v3/ldcontext"
	"github.com/launchdarkly/go-sdk-common/v3/ldvalue"

	"github.com/e2b-dev/infra/packages/shared/pkg/env"
)

// kinds
const (
	TeamKind ldcontext.Kind = "team"

	SandboxKind                        ldcontext.Kind = "sandbox"
	SandboxTemplateAttribute           string         = "template-id"
	SandboxKernelVersionAttribute      string         = "kernel-version"
	SandboxFirecrackerVersionAttribute string         = "firecracker-version"

	UserKind ldcontext.Kind = "user"

	ClusterKind ldcontext.Kind = "cluster"

	TierKind ldcontext.Kind = "tier"

	ServiceKind ldcontext.Kind = "service"

	TemplateKind ldcontext.Kind = "template"
)

// All flags must be defined here: https://app.launchdarkly.com/projects/default/flags/

type JSONFlag struct {
	name     string
	fallback ldvalue.Value
}

func (f JSONFlag) String() string {
	return f.name
}

func (f JSONFlag) Fallback() *ldvalue.Value {
	return &f.fallback
}

func newJSONFlag(name string, fallback ldvalue.Value) JSONFlag {
	flag := JSONFlag{name: name, fallback: fallback}
	builder := LaunchDarklyOfflineStore.Flag(flag.name).ValueForAll(fallback)
	LaunchDarklyOfflineStore.Update(builder)

	return flag
}

var CleanNFSCacheExperimental = newJSONFlag("clean-nfs-cache-experimental", ldvalue.Null())

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
	builder := LaunchDarklyOfflineStore.Flag(flag.name).VariationForAll(fallback)
	LaunchDarklyOfflineStore.Update(builder)

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
	builder := LaunchDarklyOfflineStore.Flag(flag.name).ValueForAll(ldvalue.Int(fallback))
	LaunchDarklyOfflineStore.Update(builder)

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
	PubsubQueueChannelSize        = newIntFlag("pubsub-queue-channel-size", 8*1024)          // size of the channel buffer used to queue incoming sandbox events
	EnvdInitTimeoutMilliseconds   = newIntFlag("envd-init-request-timeout-milliseconds", 50) // Timeout for envd init request in milliseconds
	MaxCacheWriterConcurrencyFlag = newIntFlag("max-cache-writer-concurrency", 10)

	// BuildCacheMaxUsagePercentage the maximum percentage of the cache disk storage
	// that can be used before the cache starts evicting items.
	BuildCacheMaxUsagePercentage = newIntFlag("build-cache-max-usage-percentage", 85)
	BuildProvisionVersion        = newIntFlag("build-provision-version", 0)
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
	builder := LaunchDarklyOfflineStore.Flag(flag.name).ValueForAll(ldvalue.String(fallback))
	LaunchDarklyOfflineStore.Update(builder)

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
	FirecrackerVersions     = newJSONFlag("firecracker-versions", ldvalue.FromJSONMarshal(firecrackerVersions))
)
