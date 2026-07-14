package handlers

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/orchestrator"
	"github.com/e2b-dev/infra/packages/api/internal/orchestrator/nodemanager"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	teamtypes "github.com/e2b-dev/infra/packages/auth/pkg/types"
	dbtypes "github.com/e2b-dev/infra/packages/db/pkg/types"
	sandbox_network "github.com/e2b-dev/infra/packages/shared/pkg/sandbox-network"
)

// sandboxOrchestrator is the orchestrator surface used by the API handlers. It is an
// interface (rather than the concrete *orchestrator.Orchestrator) so handlers can be
// unit-tested with a mock, in particular the snapshot fallback paths that only run
// when the orchestrator reports the sandbox is not running. It is composed from two
// role interfaces to keep each grouping focused.
type sandboxOrchestrator interface {
	sandboxOpsOrchestrator
	nodeOrchestrator
}

// sandboxOpsOrchestrator covers per-sandbox lifecycle operations.
type sandboxOpsOrchestrator interface {
	GetSandbox(ctx context.Context, teamID uuid.UUID, sandboxID string) (sandbox.Sandbox, error)
	GetSandboxes(ctx context.Context, teamID uuid.UUID, states []sandbox.State) ([]sandbox.Sandbox, error)
	KeepAliveFor(ctx context.Context, teamID uuid.UUID, sandboxID string, duration time.Duration, allowShorter bool) (*sandbox.Sandbox, *api.APIError)
	WaitForStateChange(ctx context.Context, teamID uuid.UUID, sandboxID string) error
	RemoveSandbox(ctx context.Context, teamID uuid.UUID, sandboxID string, opts sandbox.RemoveOpts) error
	CreateSandbox(
		ctx context.Context,
		sandboxID string,
		executionID string,
		team *teamtypes.Team,
		getSandboxData orchestrator.SandboxDataFetcher,
		startTime time.Time,
		endTime time.Time,
		timeout time.Duration,
		isResume bool,
		creationMeta sandbox.CreationMetadata,
	) (sandbox.Sandbox, *api.APIError)
	CreateSnapshotTemplate(ctx context.Context, teamID uuid.UUID, sandboxID string, opts orchestrator.SnapshotTemplateOpts) (orchestrator.SnapshotTemplateResult, error)
	UpdateSandboxNetworkConfig(
		ctx context.Context,
		teamID uuid.UUID,
		sandboxID string,
		allowedEntries []string,
		deniedEntries []string,
		rules map[string][]dbtypes.SandboxNetworkRule,
		allowInternetAccess *bool,
		egressProxy *sandbox_network.EgressProxyConfig,
	) *api.APIError
	HandleExistingSandboxAutoResume(ctx context.Context, teamID uuid.UUID, sandboxID string, sbx sandbox.Sandbox, transitionWaitBudget time.Duration) (string, bool, error)
	Close(ctx context.Context) error
}

// nodeOrchestrator covers node/cluster inspection used by admin and routing handlers.
type nodeOrchestrator interface {
	GetNode(clusterID uuid.UUID, nodeID string) *nodemanager.Node
	GetClusterNodes(clusterID uuid.UUID) []*nodemanager.Node
	GetNodeRouteIPAddress(clusterID uuid.UUID, nodeID string) string
	AdminNodes(clusterID uuid.UUID) ([]*api.Node, error)
	AdminNodeDetail(clusterID uuid.UUID, nodeID string) (*api.NodeDetail, error)
}

// Compile-time assertion that the concrete orchestrator satisfies the interface.
var _ sandboxOrchestrator = (*orchestrator.Orchestrator)(nil)
