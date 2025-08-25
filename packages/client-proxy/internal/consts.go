package internal

import (
	"os"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/env"
)

const (
	edgePortEnv         = "EDGE_PORT"
	edgeSecretEnv       = "EDGE_SECRET"
	proxyPortEnv        = "PROXY_PORT"
	orchestratorPortEnv = "ORCHESTRATOR_PORT"

	defaultEdgePort         = 3001
	defaultProxyPort        = 3002
	defaultOrchestratorPort = 5008
)

func GetEdgeServicePort() int {
	p, err := env.GetEnvAsInt(edgePortEnv, defaultEdgePort)
	if err != nil {
		zap.L().Fatal("Failed to get environment variable", zap.Error(err), zap.String("env", edgePortEnv))
	}

	return p
}

func GetEdgeServiceSecret() string {
	secret := os.Getenv(edgeSecretEnv)
	if secret == "" {
		zap.L().Fatal("Failed to get edge service secret")
	}

	return secret
}

func GetProxyServicePort() int {
	p, err := env.GetEnvAsInt(proxyPortEnv, defaultProxyPort)
	if err != nil {
		zap.L().Fatal("Failed to get environment variable", zap.Error(err), zap.String("env", proxyPortEnv))
	}

	return p
}

func GetOrchestratorServicePort() int {
	p, err := env.GetEnvAsInt(orchestratorPortEnv, defaultOrchestratorPort)
	if err != nil {
		zap.L().Fatal("Failed to get environment variable", zap.Error(err), zap.String("env", orchestratorPortEnv))
	}

	return p
}
