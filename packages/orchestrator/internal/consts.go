package internal

import (
	"github.com/e2b-dev/infra/packages/shared/pkg/env"
)

const (
	// Private IP don't leave the sandbox through the network bridge, so we use a reserved IP address for it:
	// See TEST-NET-3 on https://en.wikipedia.org/wiki/Reserved_IP_addresses
	defaultSandboxEventIP = "203.0.113.0"
	defaultEventProxyPort = "5010"
)

func GetSandboxEventIP() string {
	return env.GetEnv("SANDBOX_EVENT_IP", defaultSandboxEventIP)
}

func GetEventProxyPort() string {
	return env.GetEnv("SANDBOX_EVENT_PROXY_PORT", defaultEventProxyPort)
}
