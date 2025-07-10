package feature_flags

// Flag for enabling writing metrics for sandbox
// https://app.launchdarkly.com/projects/default/flags/sandbox-metrics-write
const (
	MetricsWriteFlagName = "sandbox-metrics-write"
	MetricsWriteDefault  = false
)

// Flag for enabling writing metrics for sandbox
// https://app.launchdarkly.com/projects/default/flags/sandbox-metrics-read
const (
	MetricsReadFlagName = "sandbox-metrics-read"
	MetricsReadDefault  = false
)
