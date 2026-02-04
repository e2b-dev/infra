package discovery

import (
	"context"

	"go.opentelemetry.io/otel"
)

var tracer = otel.Tracer("github.com/e2b-dev/infra/packages/api/internal/clusters/discovery")

type Item struct {
	// Identifier that uniquely identifies the instance so it will not be registered multiple times.
	UniqueIdentifier string
	NodeID           string

	// Instance ID that changes on each restart, available only for edge-backend service discovery.
	InstanceID string

	// Following fields are available only for local cluster.
	// For remote clusters gRPC proxy is used and these fields are not needed.
	LocalIPAddress       string
	LocalInstanceApiPort uint16
}

type Discovery interface {
	Query(ctx context.Context) ([]Item, error)
}
