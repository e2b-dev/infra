package pool

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/proxy/internal/edge/authorization"
	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	"github.com/e2b-dev/infra/packages/shared/pkg/http/edge"
	l "github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

const (
	edgeSyncMaxRetries = 3
)

type EdgeNodeInfo struct {
	NodeID string

	ServiceInstanceID    string
	ServiceVersion       string
	ServiceVersionCommit string
	ServiceStatus        api.ClusterNodeStatus
	ServiceStartup       time.Time

	Host string
}

type EdgeNode struct {
	info EdgeNodeInfo

	client *api.ClientWithResponses
	mutex  sync.RWMutex
}

func NewEdgeNode(host string, auth authorization.AuthorizationService) (*EdgeNode, error) {
	client, err := newEdgeApiClient(host, auth)
	if err != nil {
		return nil, fmt.Errorf("failed to create http client: %w", err)
	}

	o := &EdgeNode{
		client: client,
		info: EdgeNodeInfo{
			Host: host,
		},
	}

	return o, nil
}

func (o *EdgeNode) sync(ctx context.Context) error {
	for i := 0; i < edgeSyncMaxRetries; i++ {
		info := o.GetInfo()
		res, err := o.client.V1InfoWithResponse(ctx)
		if err != nil {
			zap.L().Error("failed to check edge node status", l.WithClusterNodeID(info.NodeID), zap.Error(err))
			continue
		}

		if res.JSON200 == nil {
			zap.L().Error("failed to check edge node status", l.WithClusterNodeID(info.NodeID), zap.Int("status", res.StatusCode()))
			continue
		}

		body := res.JSON200

		info.NodeID = body.NodeID
		info.ServiceInstanceID = body.ServiceInstanceID
		info.ServiceStartup = body.ServiceStartup
		info.ServiceStatus = body.ServiceStatus
		info.ServiceVersion = body.ServiceVersion
		info.ServiceVersionCommit = body.ServiceVersionCommit
		o.setInfo(info)

		return nil
	}

	return errors.New("failed to check edge node status")
}

func (o *EdgeNode) GetClient() *api.ClientWithResponses {
	return o.client
}

func (o *EdgeNode) GetInfo() EdgeNodeInfo {
	o.mutex.RLock()
	defer o.mutex.RUnlock()
	return o.info
}

func (o *EdgeNode) setInfo(info EdgeNodeInfo) {
	o.mutex.Lock()
	defer o.mutex.Unlock()
	o.info = info
}

func (o *EdgeNode) setStatus(s api.ClusterNodeStatus) {
	o.mutex.Lock()
	defer o.mutex.Unlock()
	o.info.ServiceStatus = s
}

func newEdgeApiClient(host string, auth authorization.AuthorizationService) (*api.ClientWithResponses, error) {
	clientURL := fmt.Sprintf("http://%s", host)
	clientSecret := auth.GetSecret()
	clientAuthMiddleware := func(c *api.Client) error {
		c.RequestEditors = append(
			c.RequestEditors,
			func(ctx context.Context, req *http.Request) error {
				req.Header.Set(consts.EdgeApiAuthHeader, clientSecret)
				return nil
			},
		)
		return nil
	}

	return api.NewClientWithResponses(clientURL, clientAuthMiddleware)
}
