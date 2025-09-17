package internal

import (
	"github.com/e2b-dev/infra/packages/shared/pkg/env"
)

const (
	// Using reserver IPv4 in range that is used for experiments and documentation
	// https://en.wikipedia.org/wiki/Reserved_IP_addresses
	defaultHyperloopIP        = "192.0.2.1"
	defaultHyperloopProxyPort = "5010"
)

func GetHyperloopIP() string {
	return env.GetEnv("SANDBOX_HYPERLOOP_IP", defaultHyperloopIP)
}

func GetHyperloopProxyPort() string {
	return env.GetEnv("SANDBOX_HYPERLOOP_PROXY_PORT", defaultHyperloopProxyPort)
}
