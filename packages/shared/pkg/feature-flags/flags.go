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

func (f BoolFlag) String() string {
	return f.name
}

func newBoolFlag(name string, fallback bool) BoolFlag {
	flag := BoolFlag{name: name, fallback: fallback}
	builder := LaunchDarklyOfflineStore.Flag(flag.name).VariationForAll(fallback)
	LaunchDarklyOfflineStore.Update(builder)

	return flag
}

var (
	MetricsWriteFlagName               = newBoolFlag("sandbox-metrics-write", env.IsDevelopment())
	MetricsReadFlagName                = newBoolFlag("sandbox-metrics-read", env.IsDevelopment())
	SnapshotFeatureFlagName            = newBoolFlag("use-nfs-for-snapshots", env.IsDevelopment())
	TemplateFeatureFlagName            = newBoolFlag("use-nfs-for-templates", env.IsDevelopment())
	BuildingFeatureFlagName            = newBoolFlag("use-nfs-for-building-templates", env.IsDevelopment())
	BestOfKCanFit                      = newBoolFlag("best-of-k-can-fit", true)
	BestOfKTooManyStarting             = newBoolFlag("best-of-k-too-many-starting", false)
	EdgeProvidedSandboxMetricsFlagName = newBoolFlag("edge-provided-sandbox-metrics", false)
)

type IntFlag struct {
	name     string
	fallback int
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
	ClickhouseBatcherMaxBatchSize = newIntFlag("clickhouse-batcher-max-batch-size", 64*1024) // 65536
	ClickhouseBatcherMaxDelay     = newIntFlag("clickhouse-batcher-max-delay", 100)          // 100ms in milliseconds
	ClickhouseBatcherQueueSize    = newIntFlag("clickhouse-batcher-queue-size", 8*1024)      // 8192
	BestOfKSampleSize             = newIntFlag("best-of-k-sample-size", 3)                   // Default K=3
	BestOfKMaxOvercommit          = newIntFlag("best-of-k-max-overcommit", 400)              // Default R=4 (stored as percentage, max over-commit ratio)
	BestOfKAlpha                  = newIntFlag("best-of-k-alpha", 50)                        // Default Alpha=0.5 (stored as percentage for int flag, current usage weight)
	PubsubQueueChannelSize        = newIntFlag("pubsub-queue-channel-size", 8*1024)          // size of the channel buffer used to queue incoming sandbox events
	EnvdInitTimeoutSeconds        = newIntFlag("envd-init-request-timeout-milliseconds", 50) // Timeout for envd init request in milliseconds

	// BuildCacheMaxUsagePercentage the maximum percentage of the cache disk storage
	// that can be used before the cache starts evicting items.
	BuildCacheMaxUsagePercentage = newIntFlag("build-cache-max-usage-percentage", 85)
	BuildProvisionVersion        = newIntFlag("build-provision-version", 0)
)

type StringFlag struct {
	name     string
	fallback string
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

// BuildIoEngine Sync is used by default as there seems to be a bad interaction between Async and a lot of io operations.
var BuildIoEngine = newStringFlag("build-io-engine", "Sync")
