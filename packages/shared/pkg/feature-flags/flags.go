package feature_flags

import (
	"github.com/e2b-dev/infra/packages/shared/pkg/env"
)

// All flags must be defined here: https://app.launchdarkly.com/projects/default/flags/

type BoolFlag string

const (
	MetricsWriteFlagName                BoolFlag = "sandbox-metrics-write"
	MetricsReadFlagName                 BoolFlag = "sandbox-metrics-read"
	SandboxLifeCycleEventsWriteFlagName BoolFlag = "sandbox-lifecycle-events-write"
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
)

var flagsBool = map[BoolFlag]bool{
	MetricsWriteFlagName:                env.IsDevelopment(),
	MetricsReadFlagName:                 env.IsDevelopment(),
	SandboxLifeCycleEventsWriteFlagName: env.IsDevelopment(),
}

var flagsInt = map[IntFlag]int{
	GcloudConcurrentUploadLimit:   8,
	GcloudMaxTasks:                16,
	ClickhouseBatcherMaxBatchSize: 64 * 1024, // 65536
	ClickhouseBatcherMaxDelay:     100,       // 100ms in milliseconds
	ClickhouseBatcherQueueSize:    8 * 1024,  // 8192
}
