package sandbox

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	sbxlogger "github.com/e2b-dev/infra/packages/shared/pkg/logger/sandbox"
)

type (
	InsertCallback func(ctx context.Context, sbx Sandbox)
	ItemsOption    func(*ItemsFilter)
)

type ItemsFilter struct {
	OnlyExpired bool
}

func NewItemsFilter() *ItemsFilter {
	return &ItemsFilter{
		OnlyExpired: false,
	}
}

type ReservationStorage interface {
	Reserve(ctx context.Context, teamID uuid.UUID, sandboxID string, limit int) (finishStart func(Sandbox, error), waitForStart func(ctx context.Context) (Sandbox, error), err error)
	Release(ctx context.Context, teamID uuid.UUID, sandboxID string) error
}

type Storage interface {
	Add(ctx context.Context, sandbox Sandbox) error
	Get(ctx context.Context, teamID uuid.UUID, sandboxID string) (Sandbox, error)
	Remove(ctx context.Context, teamID uuid.UUID, sandboxID string) error

	TeamItems(ctx context.Context, teamID uuid.UUID, states []State) ([]Sandbox, error)
	AllItems(ctx context.Context, states []State, options ...ItemsOption) ([]Sandbox, error)

	Update(ctx context.Context, teamID uuid.UUID, sandboxID string, updateFunc func(sandbox Sandbox) (Sandbox, error)) (Sandbox, error)
	StartRemoving(ctx context.Context, teamID uuid.UUID, sandboxID string, stateAction StateAction) (alreadyDone bool, callback func(context.Context, error), err error)
	WaitForStateChange(ctx context.Context, teamID uuid.UUID, sandboxID string) error
	Sync(sandboxes []Sandbox, nodeID string) []Sandbox
}

func WithOnlyExpired(isExpired bool) ItemsOption {
	return func(f *ItemsFilter) {
		f.OnlyExpired = isExpired
	}
}

type Callbacks struct {
	// AddSandboxToRoutingTable should be called sync to prevent race conditions where we would know where to route the sandbox
	AddSandboxToRoutingTable InsertCallback
	// AsyncSandboxCounter should be called async to prevent blocking the main goroutine
	AsyncSandboxCounter InsertCallback
	// AsyncNewlyCreatedSandbox should be called async to prevent blocking the main goroutine
	AsyncNewlyCreatedSandbox InsertCallback
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

func (s *Store) Add(ctx context.Context, sandbox Sandbox, newlyCreated bool) error {
	sbxlogger.I(sandbox).Debug(ctx, "Adding sandbox to cache",
		zap.Bool("newly_created", newlyCreated),
		zap.Time("start_time", sandbox.StartTime),
		zap.Time("end_time", sandbox.EndTime),
	)

	endTime := sandbox.EndTime

	if endTime.Sub(sandbox.StartTime) > sandbox.MaxInstanceLength {
		sandbox.EndTime = sandbox.StartTime.Add(sandbox.MaxInstanceLength)
	}

	err := s.storage.Add(ctx, sandbox)
	if err == nil {
		// Count only newly added sandboxes to the store
		s.callbacks.AddSandboxToRoutingTable(ctx, sandbox)
		go s.callbacks.AsyncSandboxCounter(context.WithoutCancel(ctx), sandbox)
	} else {
		// There's a race condition when the sandbox is added from node sync
		// This should be fixed once the sync is improved
		if !errors.Is(err, ErrAlreadyExists) {
			return err
		}

		logger.L().Warn(ctx, "Sandbox already exists in cache", logger.WithSandboxID(sandbox.SandboxID))
	}

	// Ensure the team reservation is set - no limit
	finishStart, _, err := s.reservations.Reserve(ctx, sandbox.TeamID, sandbox.SandboxID, -1)
	if err != nil {
		logger.L().Error(ctx, "Failed to reserve sandbox", zap.Error(err), logger.WithSandboxID(sandbox.SandboxID))
	}

	if finishStart != nil {
		finishStart(sandbox, nil)
	}

	if newlyCreated {
		go s.callbacks.AsyncNewlyCreatedSandbox(context.WithoutCancel(ctx), sandbox)
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

func (s *Store) AllItems(ctx context.Context, states []State, options ...ItemsOption) ([]Sandbox, error) {
	return s.storage.AllItems(ctx, states, options...)
}

func (s *Store) Update(ctx context.Context, teamID uuid.UUID, sandboxID string, updateFunc func(sandbox Sandbox) (Sandbox, error)) (Sandbox, error) {
	return s.storage.Update(ctx, teamID, sandboxID, updateFunc)
}

func (s *Store) StartRemoving(ctx context.Context, teamID uuid.UUID, sandboxID string, stateAction StateAction) (alreadyDone bool, callback func(context.Context, error), err error) {
	return s.storage.StartRemoving(ctx, teamID, sandboxID, stateAction)
}

func (s *Store) WaitForStateChange(ctx context.Context, teamID uuid.UUID, sandboxID string) error {
	return s.storage.WaitForStateChange(ctx, teamID, sandboxID)
}

func (s *Store) Sync(ctx context.Context, sandboxes []Sandbox, nodeID string) {
	sbxs := s.storage.Sync(sandboxes, nodeID)
	for _, sbx := range sbxs {
		err := s.Add(ctx, sbx, false)
		if err != nil {
			logger.L().Error(ctx, "Failed to re-add sandbox during sync", zap.Error(err), logger.WithSandboxID(sbx.SandboxID))
		}
	}
}

func (s *Store) Reserve(ctx context.Context, teamID uuid.UUID, sandboxID string, limit int) (finishStart func(Sandbox, error), waitForStart func(ctx context.Context) (Sandbox, error), err error) {
	return s.reservations.Reserve(ctx, teamID, sandboxID, limit)
}

func (s *Store) Release(ctx context.Context, teamID uuid.UUID, sandboxID string) error {
	return s.reservations.Release(ctx, teamID, sandboxID)
}
