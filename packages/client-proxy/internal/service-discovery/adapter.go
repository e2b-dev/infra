package service_discovery

import (
	"context"
)

type DiscoveredInstance struct {
	InstanceIPAddress string
	InstancePort      uint16
}

type ServiceDiscoveryAdapter interface {
	ListInstances(ctx context.Context) ([]DiscoveredInstance, error)
}
