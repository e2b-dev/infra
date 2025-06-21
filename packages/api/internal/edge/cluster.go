package edge

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"

	"github.com/google/uuid"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	"github.com/e2b-dev/infra/packages/api/internal/orchestrator"
	api "github.com/e2b-dev/infra/packages/shared/pkg/http/edge"
	l "github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

type Cluster struct {
	Id uuid.UUID

	httpClient *api.ClientWithResponses
	grpcClient *orchestrator.GRPCClient

	ctx       context.Context
	ctxCancel context.CancelFunc
}

const (
	edgeApiAuthHeader = "X-API-Key"
	edgeRpcAuthHeader = "authorization"
	edgeRpcNodeHeader = "node-id"
)

var (
	ErrTemplateBuilderNotFound          = errors.New("template builder not found")
	ErrAvailableTemplateBuilderNotFound = errors.New("available template builder not found")
)

func NewCluster(ctx context.Context, tel *telemetry.Client, endpoint string, endpointTls bool, secret string, id uuid.UUID) (*Cluster, error) {
	// so we during cluster disconnect we don't cancel the upper context
	ctx, ctxCancel := context.WithCancel(ctx)

	clientAuthMiddleware := func(c *api.Client) error {
		c.RequestEditors = append(
			c.RequestEditors,
			func(ctx context.Context, req *http.Request) error {
				req.Header.Set(edgeApiAuthHeader, secret)
				return nil
			},
		)
		return nil
	}

	// generate the full endpoint URL
	var endpointBaseUrl string
	if endpointTls {
		endpointBaseUrl = fmt.Sprintf("https://%s", endpoint)
	} else {
		endpointBaseUrl = fmt.Sprintf("http://%s", endpoint)
	}

	httpClient, err := api.NewClientWithResponses(endpointBaseUrl, clientAuthMiddleware)
	if err != nil {
		ctxCancel()
		return nil, fmt.Errorf("failed to create http client: %w", err)
	}

	grpcAuthorization := clientAuthorization{secret: secret, tls: endpointTls}
	grpcOptions := []grpc.DialOption{
		grpc.WithPerRPCCredentials(grpcAuthorization),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	}

	grpcClient, err := orchestrator.NewClientWithOptions(tel.TracerProvider, tel.MeterProvider, endpoint, endpointTls, grpcOptions)
	if err != nil {
		ctxCancel()
		return nil, fmt.Errorf("failed to create grpc client: %w", err)
	}

	return &Cluster{
		Id: id,

		ctx:       ctx,
		ctxCancel: ctxCancel,

		httpClient: httpClient,
		grpcClient: grpcClient,
	}, nil
}

func (c *Cluster) Disconnect() error {
	err := c.grpcClient.Close()
	c.ctxCancel()
	return err
}

// todo: nodes sync (healthy etc)
func (c *Cluster) getNodes() ([]*api.ClusterOrchestratorNode, error) {
	res, err := c.httpClient.V1ServiceDiscoveryGetOrchestratorsWithResponse(c.ctx)
	if err != nil {
		zap.L().Error("Failed to get builders", zap.Error(err), l.WithClusterID(c.Id))
		return nil, err
	}

	if res.StatusCode() != http.StatusOK {
		return nil, fmt.Errorf("failed to get builders from api: %s", res.Status())
	}

	if res.JSON200 == nil {
		return nil, errors.New("request to get builders returned nil response")
	}

	nodes := make([]*api.ClusterOrchestratorNode, 0)
	for _, o := range *res.JSON200 {
		nodes = append(nodes, &o)
	}

	return nodes, nil
}

func (c *Cluster) GetTemplateManagerNodes() ([]*api.ClusterOrchestratorNode, error) {
	nodes, err := c.getNodes()
	if err != nil {
		return nil, err
	}

	templateManagers := make([]*api.ClusterOrchestratorNode, 0)
	for _, node := range nodes {
		if node.Status == api.Unhealthy || !slices.Contains(node.Roles, api.ClusterOrchestratorRoleTemplateManager) {
			continue
		}
		templateManagers = append(templateManagers, node)
	}

	return templateManagers, nil
}

func (c *Cluster) GetTemplateManagerById(nodeID string) (*api.ClusterOrchestratorNode, error) {
	templateManagers, err := c.GetTemplateManagerNodes()
	if err != nil {
		return nil, err
	}

	for _, o := range templateManagers {
		if o.Id == nodeID {
			return o, nil
		}
	}

	return nil, ErrTemplateBuilderNotFound
}

func (c *Cluster) GetGrpcClient() *orchestrator.GRPCClient {
	return c.grpcClient
}

func (c *Cluster) GetHttpClient() *api.ClientWithResponses {
	return c.httpClient
}

func (c *Cluster) GetGrpcClientMetadata(nodeID string) metadata.MD {
	return metadata.New(map[string]string{edgeRpcNodeHeader: nodeID})
}

func (c *Cluster) GetAvailableTemplateBuilder() (*api.ClusterOrchestratorNode, error) {
	templateManagers, err := c.GetTemplateManagerNodes()
	if err != nil {
		return nil, err
	}

	for _, o := range templateManagers {
		if o.Status != api.Unhealthy {
			return o, nil
		}

		//if slices.Contains(o.Roles, api.ClusterOrchestratorRoleTemplateManager) && o.Available {
		//	return o, nil
		//}
	}

	return nil, ErrAvailableTemplateBuilderNotFound
}
