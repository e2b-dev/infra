package sandbox

import (
	"context"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	sbxlogger "github.com/e2b-dev/infra/packages/shared/pkg/logger/sandbox"
)

type (
	InsertCallback func(ctx context.Context, sbx Sandbox, created bool)
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
	Reserve(ctx context.Context, teamID, sandboxID string, limit int) (finishStart func(Sandbox, error), waitForStart func(ctx context.Context) (Sandbox, error), err error)
	Release(ctx context.Context, teamID, sandboxID string) error
}

type Storage interface {
	Add(ctx context.Context, sandbox Sandbox) error
	Get(ctx context.Context, sandboxID string) (Sandbox, error)
	Remove(ctx context.Context, sandboxID string) error

	Items(teamID *uuid.UUID, states []State, options ...ItemsOption) []Sandbox

	Update(ctx context.Context, sandboxID string, updateFunc func(sandbox Sandbox) (Sandbox, error)) (Sandbox, error)
	StartRemoving(ctx context.Context, sandboxID string, stateAction StateAction) (alreadyDone bool, callback func(error), err error)
	WaitForStateChange(ctx context.Context, sandboxID string) error
	Sync(sandboxes []Sandbox, nodeID string) []Sandbox
}

func WithOnlyExpired(isExpired bool) ItemsOption {
	return func(f *ItemsFilter) {
		f.OnlyExpired = isExpired
	}
}

type Store struct {
	storage              Storage
	insertCallbacks      []InsertCallback
	insertAsyncCallbacks []InsertCallback

	reservations ReservationStorage
}

func NewStore(
	backend Storage,
	reservations ReservationStorage,

	insertCallbacks []InsertCallback,
	insertAsyncCallbacks []InsertCallback,
) *Store {
	return &Store{
		storage:      backend,
		reservations: reservations,

		insertCallbacks:      insertCallbacks,
		insertAsyncCallbacks: insertAsyncCallbacks,
	}
}

func (s *Store) Add(ctx context.Context, sandbox Sandbox, newlyCreated bool) error {
	sbxlogger.I(sandbox).Debug("Adding sandbox to cache",
		zap.Bool("newly_created", newlyCreated),
		zap.Time("start_time", sandbox.StartTime),
		zap.Time("end_time", sandbox.EndTime),
	)

	endTime := sandbox.EndTime

	if endTime.Sub(sandbox.StartTime) > sandbox.MaxInstanceLength {
		sandbox.EndTime = sandbox.StartTime.Add(sandbox.MaxInstanceLength)
	}

	err := s.storage.Add(ctx, sandbox)
	if err != nil {
		return err
	}

	// Ensure the team reservation is set - no limit
	finishStart, _, err := s.reservations.Reserve(ctx, sandbox.TeamID.String(), sandbox.SandboxID, -1)
	if err != nil {
		zap.L().Error("Failed to reserve sandbox", zap.Error(err), logger.WithSandboxID(sandbox.SandboxID))
	}

	if finishStart != nil {
		finishStart(sandbox, nil)
	}

	// Run callbacks
	for _, callback := range s.insertCallbacks {
		callback(ctx, sandbox, newlyCreated)
	}

	for _, callback := range s.insertAsyncCallbacks {
		go callback(context.WithoutCancel(ctx), sandbox, newlyCreated)
	}

	return nil
}

func (s *Store) Get(ctx context.Context, sandboxID string) (Sandbox, error) {
	return s.storage.Get(ctx, sandboxID)
}

func (s *Store) Remove(ctx context.Context, teamID, sandboxID string) {
	err := s.storage.Remove(ctx, sandboxID)
	if err != nil {
		zap.L().Error("Failed to remove sandbox from storage", zap.Error(err), logger.WithSandboxID(sandboxID))
	}

	err = s.reservations.Release(ctx, teamID, sandboxID)
	if err != nil {
		zap.L().Error("Failed to release reservation", zap.Error(err), logger.WithSandboxID(sandboxID))
	}
}

func (s *Store) Items(teamID *uuid.UUID, states []State, options ...ItemsOption) []Sandbox {
	return s.storage.Items(teamID, states, options...)
}

func (s *Store) Update(ctx context.Context, sandboxID string, updateFunc func(sandbox Sandbox) (Sandbox, error)) (Sandbox, error) {
	return s.storage.Update(ctx, sandboxID, updateFunc)
}

func (s *Store) StartRemoving(ctx context.Context, sandboxID string, stateAction StateAction) (alreadyDone bool, callback func(error), err error) {
	return s.storage.StartRemoving(ctx, sandboxID, stateAction)
}

func (s *Store) WaitForStateChange(ctx context.Context, sandboxID string) error {
	return s.storage.WaitForStateChange(ctx, sandboxID)
}

func (s *Store) Sync(ctx context.Context, sandboxes []Sandbox, nodeID string) {
	sbxs := s.storage.Sync(sandboxes, nodeID)
	for _, sbx := range sbxs {
		err := s.Add(ctx, sbx, false)
		if err != nil {
			zap.L().Error("Failed to re-add sandbox during sync", zap.Error(err), logger.WithSandboxID(sbx.SandboxID))
		}
	}
}

func (s *Store) Reserve(ctx context.Context, teamID, sandboxID string, limit int) (finishStart func(Sandbox, error), waitForStart func(ctx context.Context) (Sandbox, error), err error) {
	return s.reservations.Reserve(ctx, teamID, sandboxID, limit)
}

func (s *Store) Release(ctx context.Context, teamID, sandboxID string) error {
	return s.reservations.Release(ctx, teamID, sandboxID)
}
