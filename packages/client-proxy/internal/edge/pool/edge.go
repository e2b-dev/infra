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

type EdgeNode struct {
	ServiceId string
	NodeId    string

	SourceVersion string
	SourceCommit  string

	Host    string
	Status  api.ClusterNodeStatus
	Startup time.Time
	Client  *api.ClientWithResponses

	mutex sync.Mutex

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
		Host:   host,
		Client: client,

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
	o.mutex.Lock()
	defer o.mutex.Unlock()

	ctx, cancel := context.WithTimeout(o.ctx, edgeSyncInterval)
	defer cancel()

	for i := 0; i < edgeSyncMaxRetries; i++ {
		res, err := o.Client.V1InfoWithResponse(ctx)
		if err != nil {
			zap.L().Error("failed to check edge node status", l.WithClusterNodeID(o.ServiceId), zap.Error(err))
			continue
		}

		if res.JSON200 == nil {
			zap.L().Error("failed to check edge node status", l.WithClusterNodeID(o.ServiceId), zap.Int("status", res.StatusCode()))
			continue
		}

		body := res.JSON200

		o.ServiceId = body.Id
		o.NodeId = body.NodeId
		o.Startup = body.Startup
		o.Status = body.Status
		o.SourceVersion = body.Version
		o.SourceCommit = body.Commit

		return nil
	}

	return errors.New("failed to check edge node status")
}

func (o *EdgeNode) Close() error {
	// close sync context
	o.ctxCancel()
	o.Status = api.Unhealthy
	return nil
}

func newEdgeApiClient(host string) (*api.ClientWithResponses, error) {
	hostUrl := fmt.Sprintf("http://%s", host)
	return api.NewClientWithResponses(hostUrl)
}
