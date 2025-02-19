package consul

import (
	"fmt"
	"sync"

	"github.com/hashicorp/consul/api"

	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

var (
	Client = sync.OnceValue(func() *api.Client {
		return utils.Must(newClient())
	})
)

func newClient() (*api.Client, error) {
	config := api.DefaultConfig()
	config.Token = utils.RequiredEnv("CONSUL_TOKEN", "Consul token for authenticating requests to the Consul API")

	consulClient, err := api.NewClient(config)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize Consul client: %w", err)
	}

	return consulClient, nil
}
