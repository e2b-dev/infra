package pool

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/http/edge"
	l "github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

const (
	edgeSyncInterval   = 10 * time.Second
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
	info   EdgeNodeInfo
	client *api.ClientWithResponses
	mutex  sync.RWMutex

	ctx       context.Context
	ctxCancel context.CancelFunc
}

func NewEdgeNode(ctx context.Context, host string) (*EdgeNode, error) {
	ctx, ctxCancel := context.WithCancel(ctx)

	client, err := newEdgeApiClient(host)
	if err != nil {
		ctxCancel()
		return nil, fmt.Errorf("failed to create http client: %w", err)
	}

	o := &EdgeNode{
		client: client,
		info: EdgeNodeInfo{
			Host: host,
		},

		ctx:       ctx,
		ctxCancel: ctxCancel,
	}

	// run the first sync immediately
	err = o.syncRun()
	if err != nil {
		return nil, fmt.Errorf("failed to fetch inital setup of edge node, maybe its not ready yet: %w", err)
	}

	// initialize background sync to update orchestrator running sandboxes
	go func() { o.sync() }()

	return o, nil
}

func (o *EdgeNode) sync() {
	ticker := time.NewTicker(orchestratorSyncInterval)
	defer ticker.Stop()

	for {
		select {
		case <-o.ctx.Done():
			return
		case <-ticker.C:
			o.syncRun()
		}
	}
}

func (o *EdgeNode) syncRun() error {
	ctx, cancel := context.WithTimeout(o.ctx, edgeSyncInterval)
	defer cancel()

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
	o.info.Status = s
}

func (o *EdgeNode) Close() error {
	// close sync context
	o.ctxCancel()
	o.setStatus(api.Unhealthy)
	return nil
}

func newEdgeApiClient(host string) (*api.ClientWithResponses, error) {
	hostUrl := fmt.Sprintf("http://%s", host)
	return api.NewClientWithResponses(hostUrl)
}
