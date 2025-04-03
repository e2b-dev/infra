package client_proxy

// User by client proxy to route traffic between nginx sandbox proxy and orchestrator proxy
// https://app.launchdarkly.com/projects/default/flags/sandbox-proxy-traffic-target
const (
	FlagName         = "sandbox-proxy-traffic-target"
	FlagDefaultValue = FlagValueNginxProxy

	FlagValueNginxProxy        = "nginx-proxy"
	FlagValueOrchestratorProxy = "orchestrator-proxy"
)
