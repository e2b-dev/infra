package service_discovery

import (
	"context"
	"errors"
	"time"
)

const (
	schemaVersion            = "v1"
	defaultMapExpiration     = 120 * time.Second
	defaultServiceExpiration = 60 * time.Second

	StatusHealthy   = "healthy"
	StatusUnhealthy = "unhealthy"
	StatusDraining  = "draining"

	ServiceTypeOrchestrator = "orchestrator"
	ServiceTypeEdge         = "edge"
)

type ServiceDiscoveryItem struct {
	SchemaVersion string `json:"schema_version"`

	ServiceType    string `json:"service_type"`
	ServiceVersion string `json:"service_version"`

	NodeIp   string `json:"node_ip"`
	NodePort int    `json:"node_port"`

	Status       string `json:"status"`
	RegisteredAt int64  `json:"registered_at"`
	ExpiresAt    int64  `json:"expires_at"`
}

var (
	NodeNotFoundErr = errors.New("node not found")
)

type ServiceDiscoveryAdapter interface {
	ListNodes(ctx context.Context) (map[string]*ServiceDiscoveryItem, error)
	GetNodeById(ctx context.Context, nodeId string) (*ServiceDiscoveryItem, error)
	GetSelfNodeId() string
	SetSelfStatus(status string)
}
