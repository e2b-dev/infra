package internal

import (
	"github.com/e2b-dev/infra/packages/shared/pkg/env"
)

const (
	// Private IP don't leave the sandbox through the network bridge, so we use a reserved IP address for it:
	// See TEST-NET-3 on https://en.wikipedia.org/wiki/Reserved_IP_addresses
	defaultSandboxHyperloopIP = "203.0.113.0"
	defaultHyperloopProxyPort = "5010"
)

func GetSandboxHyperloopIP() string {
	return env.GetEnv("SANDBOX_HYPERLOOP_IP", defaultSandboxHyperloopIP)
}

func GetHyperloopProxyPort() string {
	return env.GetEnv("SANDBOX_HYPERLOOP_PROXY_PORT", defaultHyperloopProxyPort)
}
