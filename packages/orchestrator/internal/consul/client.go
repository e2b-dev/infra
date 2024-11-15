package consul

import (
	"context"
	"fmt"

	consul "github.com/hashicorp/consul/api"

	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

var consulToken = utils.RequiredEnv("CONSUL_TOKEN", "Consul token for authenticating requests to the Consul API")

func New(ctx context.Context) (*consul.Client, error) {
	config := consul.DefaultConfig()
	config.Token = consulToken

	consulClient, err := consul.NewClient(config)
	if err != nil {
		errMsg := fmt.Errorf("failed to initialize Consul client: %w", err)
		telemetry.ReportCriticalError(ctx, errMsg)

		return nil, errMsg
	}
	return consulClient, nil
}
