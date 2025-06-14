package feature_flags

// User by client proxy to route traffic between nginx sandbox proxy and orchestrator proxy
// https://app.launchdarkly.com/projects/default/flags/sandbox-metrics-write
const (
	MetricsWriteFlagName = "sandbox-metrics-write"
	MetricsWriteDefault  = false
)
