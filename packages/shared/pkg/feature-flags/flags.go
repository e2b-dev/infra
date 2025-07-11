package feature_flags

import "github.com/e2b-dev/infra/packages/shared/pkg/env"

// Flag for enabling writing metrics for sandbox
// https://app.launchdarkly.com/projects/default/flags/sandbox-metrics-write
// https://app.launchdarkly.com/projects/default/flags/sandbox-metrics-read
const (
	MetricsWriteFlagName = "sandbox-metrics-write"
	MetricsReadFlagName  = "sandbox-metrics-read"
)

var (
	MetricsWriteDefault = env.IsDevelopment()
	MetricsReadDefault  = env.IsDevelopment()
)
