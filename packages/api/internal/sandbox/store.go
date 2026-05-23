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
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	sbxlogger "github.com/e2b-dev/infra/packages/shared/pkg/logger/sandbox"
)

type CreationMetadata struct {
	IsResume       bool
	TeamName       string
	RequestHeader  http.Header
	MCPServerNames []string
}

type (
	InsertCallback   func(ctx context.Context, sbx Sandbox)
	RemoveCallback   func(ctx context.Context, sbx Sandbox)
	CreationCallback func(ctx context.Context, sbx Sandbox, meta CreationMetadata)
)

const sbxRemoveTimeout = 10 * time.Second

// Storage names are re-exported from sandboxtypes for callers using this package.
const (
	StorageNameMemory        = sandboxtypes.StorageNameMemory
	StorageNameRedis         = sandboxtypes.StorageNameRedis
	StorageNamePopulateRedis = sandboxtypes.StorageNamePopulateRedis
)

// Storage and ReservationStorage are re-exported from sandboxtypes so external
// callers can continue to use sandbox.Storage / sandbox.ReservationStorage.
// They live in sandboxtypes (a leaf package) so storage backends like
// sandbox/storage/memory can implement them without creating an import cycle
// back into package sandbox.
type (
	Storage            = sandboxtypes.Storage
	ReservationStorage = sandboxtypes.ReservationStorage
)

type Callbacks struct {
	// AddSandboxToRoutingTable should be called sync to prevent race conditions where we would know where to route the sandbox
	AddSandboxToRoutingTable InsertCallback
	// AsyncNewlyCreatedSandbox is called asynchronously for newly created sandboxes (Add called with non-nil CreationMetadata).
	AsyncNewlyCreatedSandbox CreationCallback
	// RemoveSandboxFromNode kills an orphaned sandbox on the orchestrator node via gRPC.
	// Used during sync when the Redis backend detects sandboxes running on a node but not present in the store.
	RemoveSandboxFromNode RemoveCallback
}

type Store struct {
	storage   Storage
	callbacks Callbacks

	reservations ReservationStorage
}

func NewStore(
	backend Storage,
	reservations ReservationStorage,
	callbacks Callbacks,
) *Store {
	return &Store{
		storage:      backend,
		reservations: reservations,
		callbacks:    callbacks,
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

	endTime := sandbox.EndTime

	if endTime.Sub(sandbox.StartTime) > sandbox.MaxInstanceLength {
		sandbox.EndTime = sandbox.StartTime.Add(sandbox.MaxInstanceLength)
	}

	err := s.storage.Add(ctx, sandbox)
	if err == nil {
		// Count only newly added sandboxes to the store
		s.callbacks.AddSandboxToRoutingTable(ctx, sandbox)
	} else {
		// TODO [ENG-3514]: Remove once migrated to Redis
		// There's a race condition when the sandbox is added from node sync
		// This should be fixed once the sync is improved
		if !errors.Is(err, ErrAlreadyExists) {
			return err
		}

		logger.L().Warn(ctx, "Sandbox already exists in cache", logger.WithSandboxID(sandbox.SandboxID))
	}

	// TODO [ENG-3514]: Simplify once migrated to Redis
	// Ensure the team reservation is set - no limit.
	if s.storage.Name() != StorageNameRedis {
		finishStart, _, err := s.reservations.Reserve(ctx, sandbox.TeamID, sandbox.SandboxID, -1)
		if err != nil {
			logger.L().Error(ctx, "Failed to reserve sandbox", zap.Error(err), logger.WithSandboxID(sandbox.SandboxID))
		}

		if finishStart != nil {
			finishStart(sandbox, nil)
		}
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
	return s.storage.StartRemoving(ctx, teamID, sandboxID, opts)
}

func (s *Store) WaitForStateChange(ctx context.Context, teamID uuid.UUID, sandboxID string) error {
	return s.storage.WaitForStateChange(ctx, teamID, sandboxID)
}

func (s *Store) Reconcile(ctx context.Context, sandboxes []Sandbox, nodeID string) {
	sbxsToBeSynced := s.storage.Reconcile(ctx, sandboxes, nodeID)

	if s.storage.Name() == StorageNameRedis {
		// Redis is the source of truth — divergent sandboxes are orphans running
		// on the node but not present in the store. Kill them.
		wg := sync.WaitGroup{}
		for _, sbx := range sbxsToBeSynced {
			wg.Go(func() {
				ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), sbxRemoveTimeout)
				defer cancel()
				s.callbacks.RemoveSandboxFromNode(ctx, sbx)
			})
		}

		wg.Wait()
	} else {
		// Memory backend — divergent sandboxes are ones discovered on the node
		// that aren't in the local cache yet. Re-add them.
		for _, sbx := range sbxsToBeSynced {
			err := s.Add(ctx, sbx, nil)
			if err != nil {
				logger.L().Error(ctx, "Failed to re-add sandbox during sync", zap.Error(err), logger.WithSandboxID(sbx.SandboxID))
			}
		}
	}
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
