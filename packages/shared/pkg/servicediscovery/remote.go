package servicediscovery

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"

	api "github.com/e2b-dev/infra/packages/shared/pkg/http/edge"
)

// remoteDiscovery asks another cluster's edge API which instances it is
// running, for clusters this process cannot enumerate itself. It is the one
// backend whose identity comes from the remote side rather than from a
// scheduler: the service instance id is already per-run, so it is the ID
// directly.
type remoteDiscovery struct {
	NoSync

	client *api.ClientWithResponses
}

// NewRemote creates a Discoverer backed by a remote cluster's edge API.
func NewRemote(client *api.ClientWithResponses) Discoverer {
	return &remoteDiscovery{client: client}
}

func (d *remoteDiscovery) ListInstances(ctx context.Context) ([]Instance, error) {
	ctx, span := tracer.Start(ctx, "list-remote-cluster-instances")
	defer span.End()

	res, err := d.client.V1ServiceDiscoveryWithResponse(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting cluster instances from service discovery: %w", err)
	}
	if res.StatusCode() != http.StatusOK {
		return nil, fmt.Errorf("getting instances from edge api: %s", res.Status())
	}
	if res.JSON200 == nil {
		return nil, errors.New("request for instances returned a nil response")
	}

	out := make([]Instance, 0, len(res.JSON200.Orchestrators))
	for _, o := range res.JSON200.Orchestrators {
		out = append(out, Instance{
			WorkloadID: o.ServiceInstanceID,
			NodeID:     o.NodeID,
			IPAddress:  hostWithoutPort(o.ServiceHost),
			// Which scheduler runs it is the remote cluster's business and its
			// edge API does not report one, so this names how we heard of it.
			Backend: BackendRemote,
			// The edge API reports a host, not a control-plane port: a remote
			// cluster is reached for data-plane routing, never dialled here.
		})
	}

	return out, nil
}

func hostWithoutPort(serviceHost string) string {
	serviceHost = strings.TrimSpace(serviceHost)
	if serviceHost == "" {
		return ""
	}
	if host, _, err := net.SplitHostPort(serviceHost); err == nil {
		return host
	}

	return serviceHost
}
