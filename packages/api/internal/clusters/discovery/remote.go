package discovery

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"

	api "github.com/e2b-dev/infra/packages/shared/pkg/http/edge"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

type RemoteServiceDiscovery struct {
	clusterID uuid.UUID
	client    *api.ClientWithResponses
}

func NewRemoteServiceDiscovery(clusterID uuid.UUID, client *api.ClientWithResponses) Discovery {
	return &RemoteServiceDiscovery{
		clusterID: clusterID,
		client:    client,
	}
}

func (sd *RemoteServiceDiscovery) Query(ctx context.Context) ([]Item, error) {
	ctx, span := tracer.Start(ctx, "query-remote-cluster-nodes", trace.WithAttributes(telemetry.WithClusterID(sd.clusterID)))
	defer span.End()

	res, err := sd.client.V1ServiceDiscoveryWithResponse(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get cluster instances from service discovery: %w", err)
	}

	if res.StatusCode() != http.StatusOK {
		return nil, fmt.Errorf("failed to get nodes from edge api: %s", res.Status())
	}

	if res.JSON200 == nil {
		return nil, errors.New("request to get nodes returned nil response")
	}

	nodes := res.JSON200.Orchestrators
	result := make([]Item, len(nodes))
	for i, n := range nodes {
		result[i] = Item{
			UniqueIdentifier: n.ServiceInstanceID,
			NodeID:           n.NodeID,
			InstanceID:       n.ServiceInstanceID,
		}
	}

	return result, nil
}
