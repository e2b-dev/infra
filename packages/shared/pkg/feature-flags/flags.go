package feature_flags

import "github.com/e2b-dev/infra/packages/shared/pkg/env"

// Flag for enabling writing metrics for sandbox
// https://app.launchdarkly.com/projects/default/flags/sandbox-metrics-write
const (
	MetricsWriteFlagName = "sandbox-metrics-write"
)

var MetricsWriteDefault = env.IsDevelopment()

// Flag for enabling writing metrics for sandbox
// https://app.launchdarkly.com/projects/default/flags/sandbox-metrics-read
const (
	MetricsReadFlagName = "sandbox-metrics-read"
)

var MetricsReadDefault = env.IsDevelopment()
