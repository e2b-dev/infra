package consul

import (
	"context"
	"fmt"
	"os"

	consul "github.com/hashicorp/consul/api"
)

var consulToken = os.Getenv("CONSUL_TOKEN")

func New(ctx context.Context) (*consul.Client, error) {
	config := consul.DefaultConfig()
	config.Token = consulToken

	client, err := consul.NewClient(config)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize Consul client: %w", err)
	}

	return client, nil
}
