package orchestrator

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/launchdarkly/go-server-sdk/v7/testhelpers/ldtestdata"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/metric/noop"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/orchestrator/nodemanager"
	"github.com/e2b-dev/infra/packages/api/internal/orchestrator/placement"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox/reservations"
	sandboxmemory "github.com/e2b-dev/infra/packages/api/internal/sandbox/storage/memory"
	teamtypes "github.com/e2b-dev/infra/packages/auth/pkg/types"
	authqueries "github.com/e2b-dev/infra/packages/db/pkg/auth/queries"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
)

// testBuild returns a minimal queries.EnvBuild that satisfies CreateSandbox.
func testBuild() queries.EnvBuild {
	diskSize := int64(1024)
	envdVer := "0.1.0"

	return queries.EnvBuild{
		ID:                 uuid.New(),
		Vcpu:               2,
		RamMb:              512,
		TotalDiskSizeMb:    &diskSize,
		KernelVersion:      "5.10",
		FirecrackerVersion: "v1.7.0_abc1234",
		EnvdVersion:        &envdVer,
	}
}

// newCreateSandboxTestOrchestrator constructs the minimal Orchestrator needed for
// CreateSandbox. The returned node is already registered.
func newCreateSandboxTestOrchestrator(t *testing.T) (*Orchestrator, *nodemanager.Node) {
	t.Helper()

	store := sandbox.NewStore(
		sandboxmemory.NewStorage(),
		reservations.NewReservationStorage(),
		sandbox.Callbacks{
			AddSandboxToRoutingTable: func(context.Context, sandbox.Sandbox) {},
			AsyncNewlyCreatedSandbox: func(context.Context, sandbox.Sandbox) {},
		},
	)

	meter := noop.NewMeterProvider().Meter("github.com/e2b-dev/infra/packages/api/internal/orchestrator")
	counter, _ := meter.Int64Counter("test-created-sandboxes")

	ffClient, err := featureflags.NewClientWithDatasource(ldtestdata.DataSource())
	require.NoError(t, err)

	algo := placement.NewBestOfK(placement.DefaultBestOfKConfig()).(*placement.BestOfK)

	node := nodemanager.NewTestNode("node-1", api.NodeStatusReady, 0, 8)
	node.ClusterID = uuid.Nil // match consts.LocalClusterID (fallback when team.ClusterID is nil)

	o := &Orchestrator{
		sandboxStore:            store,
		nodes:                   smap.New[*nodemanager.Node](),
		placementAlgorithm:      algo,
		featureFlagsClient:      ffClient,
		createdSandboxesCounter: counter,
	}

	o.registerNode(node)

	return o, node
}

func testTeam() *teamtypes.Team {
	return &teamtypes.Team{
		Team: &authqueries.Team{
			ID:   uuid.New(),
			Name: "test-team",
		},
		Limits: &teamtypes.TeamLimits{
			SandboxConcurrency: 10,
			MaxLengthHours:     24,
		},
	}
}

// TestCreateSandbox_StaleDataAfterConcurrentPause exercises CreateSandbox with
// two sequential resume attempts where the snapshot changes between them.
//
// Because CreateSandbox accepts a SandboxDataFetcher callback that is invoked
// AFTER the concurrency lock is acquired, the second call reads the fresh V2
// data even though the snapshot was mutated between calls.
func TestCreateSandbox_StaleDataAfterConcurrentPause(t *testing.T) {
	t.Parallel()

	t.Run("lazy fetcher reads fresh data after lock", func(t *testing.T) {
		t.Parallel()

		o, _ := newCreateSandboxTestOrchestrator(t)
		team := testTeam()
		sandboxID := "sbx-race-" + uuid.New().String()[:8]
		build := testBuild()
		now := time.Now()

		// Mutable snapshot source simulating the cache.
		type snapshot struct {
			templateID string
			metadata   map[string]string
		}

		snap := &snapshot{
			templateID: "tpl-v1",
			metadata:   map[string]string{"snapshot": "v1"},
		}

		// The fetcher closure captures the mutable snap pointer and reads
		// current values at call time (after Reserve() acquires the lock).
		makeFetcher := func() SandboxDataFetcher {
			return func(_ context.Context) (SandboxMetadata, *api.APIError) {
				return SandboxMetadata{
					TemplateID:     snap.templateID,
					BaseTemplateID: "base-tpl",
					Metadata:       snap.metadata,
					Build:          build,
				}, nil
			}
		}

		// Resume-1: fetcher will read V1.
		sbx1, apiErr := o.CreateSandbox(
			t.Context(),
			sandboxID,
			uuid.New().String(),
			team,
			makeFetcher(),
			now,
			now.Add(time.Hour),
			time.Hour,
			true,
		)
		require.Nil(t, apiErr)
		assert.Equal(t, "tpl-v1", sbx1.TemplateID)

		// Clean up reservation.
		o.sandboxStore.Remove(t.Context(), team.Team.ID, sandboxID)

		// Snapshot changes to V2.
		snap.templateID = "tpl-v2"
		snap.metadata = map[string]string{"snapshot": "v2"}

		// Resume-2: fetcher will read V2 because it runs after Reserve().
		sbx2, apiErr := o.CreateSandbox(
			t.Context(),
			sandboxID,
			uuid.New().String(),
			team,
			makeFetcher(),
			now,
			now.Add(time.Hour),
			time.Hour,
			true,
		)
		require.Nil(t, apiErr)

		// The sandbox SHOULD have been created with V2 (fresh) data.
		assert.Equal(t, "tpl-v2", sbx2.TemplateID,
			"CreateSandbox must use the latest snapshot data, not stale pre-lock values")
		assert.Equal(t, "v2", sbx2.Metadata["snapshot"],
			"CreateSandbox must use the latest metadata, not stale pre-lock values")
	})
}
