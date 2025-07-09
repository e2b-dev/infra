package service_discovery

import (
	"context"
)

type ServiceDiscoveryItem struct {
	NodeIP   string `json:"node_ip"`
	NodePort int    `json:"node_port"`
}

type ServiceDiscoveryAdapter interface {
	ListNodes(ctx context.Context) ([]ServiceDiscoveryItem, error)
}
