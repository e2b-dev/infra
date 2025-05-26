package service_discovery

import (
	"context"
	"errors"
)

type ServiceDiscoveryItem struct {
	NodeIp   string `json:"node_ip"`
	NodePort int    `json:"node_port"`
}

var (
	NodeNotFoundErr = errors.New("node not found")
)

type ServiceDiscoveryAdapter interface {
	ListNodes(ctx context.Context) ([]*ServiceDiscoveryItem, error)
}
