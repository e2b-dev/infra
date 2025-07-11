package internal

import (
	"github.com/e2b-dev/infra/packages/shared/pkg/env"
)

const (
	defaultSandboxEventIP = "203.0.113.0"
)

func GetSandboxEventIP() string {
	return env.GetEnv("SANDBOX_EVENT_IP", defaultSandboxEventIP)
}
