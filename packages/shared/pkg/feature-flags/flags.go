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
)

var flagsBool = map[BoolFlag]bool{
	MetricsWriteFlagName:                env.IsDevelopment(),
	MetricsReadFlagName:                 env.IsDevelopment(),
	SandboxLifeCycleEventsWriteFlagName: env.IsDevelopment(),
}

var flagsInt = map[IntFlag]int{
	GcloudConcurrentUploadLimit: 8,
	GcloudMaxTasks:              16,
}
