package internal

import (
	"github.com/e2b-dev/infra/packages/shared/pkg/env"
)

const (
	// Same range used by metadata services in Firecracker/EC2
	// https://en.m.wikipedia.org/wiki/Link-local_address
	defaultSandboxEventIP = "169.254.0.7"
	defaultEventProxyPort = "5010"
)

func GetSandboxEventIP() string {
	return env.GetEnv("SANDBOX_EVENT_IP", defaultSandboxEventIP)
}

func GetEventProxyPort() string {
	return env.GetEnv("SANDBOX_EVENT_PROXY_PORT", defaultEventProxyPort)
}
