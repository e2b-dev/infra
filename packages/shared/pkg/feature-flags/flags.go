package feature_flags

import (
	"github.com/e2b-dev/infra/packages/shared/pkg/env"
)

// All flags must be defined here: https://app.launchdarkly.com/projects/default/flags/

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
	MetricsWriteFlagName                = newBoolFlag("sandbox-metrics-write", env.IsDevelopment())
	MetricsReadFlagName                 = newBoolFlag("sandbox-metrics-read", env.IsDevelopment())
	SandboxLifeCycleEventsWriteFlagName = newBoolFlag("sandbox-lifecycle-events-write", env.IsDevelopment())
	SnapshotFeatureFlagName             = newBoolFlag("use-nfs-for-snapshots", env.IsDevelopment())
	TemplateFeatureFlagName             = newBoolFlag("use-nfs-for-templates", env.IsDevelopment())
	SandboxEventsPublishFlagName        = newBoolFlag("sandbox-events-publish", env.IsDevelopment())
)

type IntFlag string

const (
	// GcloudConcurrentUploadLimit - the maximum number of concurrent uploads to GCloud
	GcloudConcurrentUploadLimit IntFlag = "gcloud-concurrent-upload-limit"
	// GcloudMaxTasks - maximum concurrent tasks for GCloud uploads
	GcloudMaxTasks IntFlag = "gcloud-max-tasks"
	// ClickhouseMaxBatchSize - maximum number of sandbox events to batch before flushing
	ClickhouseBatcherMaxBatchSize IntFlag = "clickhouse-batcher-max-batch-size"
	// ClickhouseMaxDelay - maximum time to wait for a batch to fill up before flushing it,
	// even if the batch size hasn't reached ClickhouseMaxBatchSize
	ClickhouseBatcherMaxDelay IntFlag = "clickhouse-batcher-max-delay"
	// ClickhouseQueueSize - size of the channel buffer used to queue incoming sandbox events
	ClickhouseBatcherQueueSize IntFlag = "clickhouse-batcher-queue-size"
	// PubsubQueueChannelSize - size of the channel buffer used to queue incoming sandbox events
	PubsubQueueChannelSize IntFlag = "pubsub-queue-channel-size"
)

var flagsInt = map[IntFlag]int{
	GcloudConcurrentUploadLimit:   8,
	GcloudMaxTasks:                16,
	ClickhouseBatcherMaxBatchSize: 64 * 1024, // 65536
	ClickhouseBatcherMaxDelay:     100,       // 100ms in milliseconds
	ClickhouseBatcherQueueSize:    8 * 1024,  // 8192
	PubsubQueueChannelSize:        8 * 1024,  // 8192
}
