// inspector_client.go — orchestrator-side Connect-RPC client for the
// in-guest InspectorService introduced in PR 1 (issue #2580).
//
// Mirrors the call pattern in
// packages/orchestrator/pkg/template/build/sandboxtools/command.go:155
// for the Process service.

package server

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"connectrpc.com/connect"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/proxy"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/envd/inspector"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/envd/inspector/inspectorconnect"
)

// inspectorRPCTimeout caps how long the orchestrator waits for the
// in-guest inspector to answer. The whole point of this code path is
// to avoid pause cost, so we'd rather fall through to a full
// checkpoint than block.
const inspectorRPCTimeout = 750 * time.Millisecond

// inspectorClient is a thin Connect-RPC wrapper rooted at the per-VM
// envd inspector endpoint.
type inspectorClient struct {
	client    inspectorconnect.InspectorServiceClient
	sandboxID string
	proxyHost string
}

func newInspectorClient(p *proxy.SandboxProxy, sandboxID string) *inspectorClient {
	hc := &http.Client{
		Timeout:   inspectorRPCTimeout,
		Transport: sandbox.SandboxHttpTransport,
	}
	proxyHost := fmt.Sprintf("http://localhost%s", p.GetAddr())
	return &inspectorClient{
		client:    inspectorconnect.NewInspectorServiceClient(hc, proxyHost),
		sandboxID: sandboxID,
		proxyHost: proxyHost,
	}
}

// QueryChanges returns the inspector's view. ok=false means the call
// errored or timed out and the caller should fall through to a full
// checkpoint.
func (c *inspectorClient) QueryChanges(ctx context.Context) (*inspector.QueryChangesResponse, bool) {
	ctx, cancel := context.WithTimeout(ctx, inspectorRPCTimeout)
	defer cancel()

	req := connect.NewRequest(&inspector.QueryChangesRequest{})
	if err := grpc.SetSandboxHeader(req.Header(), c.proxyHost, c.sandboxID); err != nil {
		return nil, false
	}

	resp, err := c.client.QueryChanges(ctx, req)
	if err != nil {
		return nil, false
	}
	return resp.Msg, true
}

// ResetEpoch advances the inspector's epoch baseline. Best-effort:
// errors are swallowed because a missed reset only causes the next
// query to over-report changes (i.e. fall through to a full
// checkpoint), which is a no-op vs. the historical behavior.
func (c *inspectorClient) ResetEpoch(ctx context.Context, expectedEpochID uint32) {
	ctx, cancel := context.WithTimeout(ctx, inspectorRPCTimeout)
	defer cancel()

	req := connect.NewRequest(&inspector.ResetEpochRequest{
		ExpectedEpochId: expectedEpochID,
	})
	if err := grpc.SetSandboxHeader(req.Header(), c.proxyHost, c.sandboxID); err != nil {
		return
	}
	_, _ = c.client.ResetEpoch(ctx, req)
}
