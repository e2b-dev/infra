package sandboxtypes

import (
	"context"

	"github.com/google/uuid"
)

const (
	StorageNameRedis = "redis"
)

// Storage is the persistence interface implemented by the redis backend.
type Storage interface {
	Add(ctx context.Context, sandbox Sandbox) error
	Get(ctx context.Context, teamID uuid.UUID, sandboxID string) (Sandbox, error)
	Remove(ctx context.Context, teamID uuid.UUID, sandboxID string) error

	TeamItems(ctx context.Context, teamID uuid.UUID, states []State) ([]Sandbox, error)
	ExpiredItems(ctx context.Context) ([]Sandbox, error)
	TeamsWithSandboxCount(ctx context.Context) (map[uuid.UUID]int64, error)

	Update(ctx context.Context, teamID uuid.UUID, sandboxID string, updateFunc func(sandbox Sandbox) (Sandbox, error)) (Sandbox, error)
	StartRemoving(ctx context.Context, teamID uuid.UUID, sandboxID string, opts RemoveOpts) (Sandbox, bool, func(context.Context, error), error)
	WaitForStateChange(ctx context.Context, teamID uuid.UUID, sandboxID string) error
	Reconcile(ctx context.Context, sandboxes []Sandbox, nodeID string) []Sandbox
}

// ReservationStorage tracks per-team sandbox-start reservations to enforce
// concurrency limits.
type ReservationStorage interface {
	Reserve(ctx context.Context, teamID uuid.UUID, sandboxID string, limit int) (finishStart func(Sandbox, error), waitForStart func(ctx context.Context) (Sandbox, error), err error)
	Release(ctx context.Context, teamID uuid.UUID, sandboxID string) error
}
