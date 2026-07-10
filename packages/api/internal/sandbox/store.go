package sandbox

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/sandbox/sandboxtypes"
	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	sbxlogger "github.com/e2b-dev/infra/packages/shared/pkg/logger/sandbox"
	e2bcatalog "github.com/e2b-dev/infra/packages/shared/pkg/sandbox-catalog"
)

type CreationMetadata struct {
	IsResume       bool
	TeamName       string
	RequestHeader  http.Header
	MCPServerNames []string
}

type (
	RemoveCallback   func(ctx context.Context, sbx Sandbox)
	CreationCallback func(ctx context.Context, sbx Sandbox, meta CreationMetadata)
)

const sbxRemoveTimeout = 10 * time.Second

// Storage and ReservationStorage are re-exported from sandboxtypes so external
// callers can continue to use sandbox.Storage / sandbox.ReservationStorage.
// They live in sandboxtypes (a leaf package) so storage backends can implement
// them without creating an import cycle back into package sandbox.
type (
	Storage            = sandboxtypes.Storage
	ReservationStorage = sandboxtypes.ReservationStorage
)

type Callbacks struct {
	// AsyncNewlyCreatedSandbox is called asynchronously for newly created sandboxes (Add called with non-nil CreationMetadata).
	AsyncNewlyCreatedSandbox CreationCallback
	// RemoveSandboxFromNode kills an orphaned sandbox on the orchestrator node via gRPC.
	// Used during sync when the Redis backend detects sandboxes running on a node but not present in the store.
	RemoveSandboxFromNode RemoveCallback
}

type Store struct {
	storage        Storage
	routingCatalog e2bcatalog.SandboxesCatalog
	callbacks      Callbacks

	reservations ReservationStorage
}

func NewStore(
	backend Storage,
	reservations ReservationStorage,
	routingCatalog e2bcatalog.SandboxesCatalog,
	callbacks Callbacks,
) *Store {
	return &Store{
		storage:        backend,
		reservations:   reservations,
		routingCatalog: routingCatalog,
		callbacks:      callbacks,
	}
}

// Add inserts a sandbox into the store. A non-nil creation argument fires the
// AsyncNewlyCreatedSandbox callback; nil indicates a sync/reconcile re-add.
func (s *Store) Add(ctx context.Context, sandbox Sandbox, creation *CreationMetadata) error {
	sbxlogger.I(sandbox).Debug(ctx, "Adding sandbox to cache",
		zap.Bool("newly_created", creation != nil),
		logger.Time("start_time", sandbox.StartTime),
		logger.Time("end_time", sandbox.EndTime),
	)

	if sandbox.EndTime.Sub(sandbox.StartTime) > sandbox.MaxInstanceLength {
		sandbox.EndTime = sandbox.StartTime.Add(sandbox.MaxInstanceLength)
	}

	if err := s.storage.Add(ctx, sandbox); err != nil {
		return err
	}

	if creation != nil {
		meta := *creation
		go s.callbacks.AsyncNewlyCreatedSandbox(context.WithoutCancel(ctx), sandbox, meta)
	}

	return nil
}

func (s *Store) Get(ctx context.Context, teamID uuid.UUID, sandboxID string) (Sandbox, error) {
	return s.storage.Get(ctx, teamID, sandboxID)
}

func (s *Store) Remove(ctx context.Context, teamID uuid.UUID, sandboxID string) {
	err := s.storage.Remove(ctx, teamID, sandboxID)
	if err != nil {
		logger.L().Error(ctx, "Failed to remove sandbox from storage", zap.Error(err), logger.WithSandboxID(sandboxID))
	}

	err = s.reservations.Release(ctx, teamID, sandboxID)
	if err != nil {
		logger.L().Error(ctx, "Failed to release reservation", zap.Error(err), logger.WithSandboxID(sandboxID))
	}
}

func (s *Store) TeamItems(ctx context.Context, teamID uuid.UUID, states []State) ([]Sandbox, error) {
	return s.storage.TeamItems(ctx, teamID, states)
}

func (s *Store) ExpiredItems(ctx context.Context) ([]Sandbox, error) {
	return s.storage.ExpiredItems(ctx)
}

func (s *Store) TeamsWithSandboxes(ctx context.Context) (map[uuid.UUID]int64, error) {
	return s.storage.TeamsWithSandboxCount(ctx)
}

func (s *Store) Update(ctx context.Context, teamID uuid.UUID, sandboxID string, updateFunc func(sandbox Sandbox) (Sandbox, error)) (Sandbox, error) {
	return s.storage.Update(ctx, teamID, sandboxID, updateFunc)
}

func (s *Store) StartRemoving(ctx context.Context, teamID uuid.UUID, sandboxID string, opts RemoveOpts) (Sandbox, bool, func(context.Context, error), error) {
	sandbox, alreadyDone, finish, err := s.storage.StartRemoving(ctx, teamID, sandboxID, opts)

	// Routing is set on new catalog-managed records. Local cluster ID covers
	// records created before routing metadata was persisted.
	if err == nil && opts.Action.Effect == TransitionExpires &&
		(sandbox.Routing != nil || sandbox.ClusterID == consts.LocalClusterID) {
		if routeErr := s.routingCatalog.DeleteSandbox(ctx, sandbox.SandboxID, sandbox.ExecutionID); routeErr != nil {
			logger.L().Error(ctx, "error removing routing record from catalog", zap.Error(routeErr), logger.WithSandboxID(sandbox.SandboxID))
		}
	}

	return sandbox, alreadyDone, finish, err
}

func (s *Store) WaitForStateChange(ctx context.Context, teamID uuid.UUID, sandboxID string) error {
	return s.storage.WaitForStateChange(ctx, teamID, sandboxID)
}

func (s *Store) Reconcile(ctx context.Context, sandboxes []Sandbox, nodeID string) {
	// Redis is the source of truth — divergent sandboxes are orphans running
	// on the node but not present in the store. Kill them.
	orphans := s.storage.Reconcile(ctx, sandboxes, nodeID)

	wg := sync.WaitGroup{}
	for _, sbx := range orphans {
		wg.Go(func() {
			ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), sbxRemoveTimeout)
			defer cancel()
			s.callbacks.RemoveSandboxFromNode(ctx, sbx)
		})
	}

	wg.Wait()
}

func (s *Store) Reserve(ctx context.Context, teamID uuid.UUID, sandboxID string, limit int) (finishStart func(Sandbox, error), waitForStart func(ctx context.Context) (Sandbox, error), err error) {
	finishStart, waitForStart, err = s.reservations.Reserve(ctx, teamID, sandboxID, limit)
	if err != nil {
		if errors.Is(err, ErrAlreadyExists) {
			// Try to get the sandbox from the storage if already exists
			return nil, func(ctx context.Context) (Sandbox, error) {
				return s.storage.Get(ctx, teamID, sandboxID)
			}, nil
		}

		return nil, nil, err
	}

	return finishStart, waitForStart, nil
}

func (s *Store) Close(ctx context.Context) error {
	return s.routingCatalog.Close(ctx)
}
