package ioc

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/google/uuid"
	"go.uber.org/fx"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/cfg"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/service"
	"github.com/e2b-dev/infra/packages/shared/pkg/env"
	featureflags "github.com/e2b-dev/infra/packages/shared/pkg/feature-flags"
)

func newServiceInfo(state State, config cfg.Config, version VersionInfo) *service.ServiceInfo {
	nodeID := state.NodeID
	serviceInstanceID := state.ServiceInstanceID

	return service.NewInfoContainer(nodeID, version.Version, version.Commit, serviceInstanceID, config)
}

func newFeatureFlagsClient(lc fx.Lifecycle) (*featureflags.Client, error) {
	ff, err := featureflags.NewClient()
	if err != nil {
		return nil, fmt.Errorf("failed to create feature flags client: %w", err)
	}
	lc.Append(fx.Hook{
		OnStop: func(ctx context.Context) error {
			return ff.Close(ctx)
		},
	})

	return ff, nil
}

type State struct {
	Services          []cfg.ServiceType
	NodeID            string
	ServiceName       string
	ServiceInstanceID string
}

func newState(config cfg.Config) State {
	services := cfg.GetServices(config)
	nodeID := env.GetNodeID()
	serviceName := cfg.GetServiceName(services)
	serviceInstanceID := uuid.NewString()

	return State{
		Services:          services,
		NodeID:            nodeID,
		ServiceName:       serviceName,
		ServiceInstanceID: serviceInstanceID,
	}
}

func NewConfig() (cfg.Config, error) {
	config, err := cfg.Parse()
	if err != nil {
		log.Fatalf("failed to parse config: %v", err)
	}

	for _, dir := range []string{
		config.DefaultCacheDir,
		config.OrchestratorBaseDir,
		config.SandboxCacheDir,
		config.SandboxDir,
		config.SharedChunkCacheDir,
		config.SnapshotCacheDir,
		config.TemplateCacheDir,
		config.TemplatesDir,
	} {
		if dir == "" {
			continue
		}

		if err := os.MkdirAll(dir, 0o700); err != nil {
			return config, fmt.Errorf("failed to make %q: %w", dir, err)
		}
	}

	return config, nil
}
