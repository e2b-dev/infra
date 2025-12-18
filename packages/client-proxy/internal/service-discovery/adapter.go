package service_discovery

import (
	"context"
)

type ServiceDiscoveryItem struct {
	InstanceIPAddress string
	InstancePort      uint16
}

type ServiceDiscoveryAdapter interface {
	ListInstances(ctx context.Context) ([]ServiceDiscoveryItem, error)
}
