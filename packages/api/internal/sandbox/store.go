package sandbox

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type (
	InsertCallback func(ctx context.Context, sbx Sandbox, created bool)
)

type Store interface {
	Add(ctx context.Context, sandbox Sandbox, newlyCreated bool)
	Get(sandboxID string, includeEvicting bool) (Sandbox, error)
	Remove(sandboxID string)
	Items(teamID *uuid.UUID) []Sandbox
	ItemsToEvict() []Sandbox
	ItemsByState(teamID *uuid.UUID, states []State) map[State][]Sandbox
	ExtendEndTime(sandboxID string, newEndTime time.Time, allowShorter bool) (bool, error)
	StartRemoving(ctx context.Context, sandboxID string, stateAction StateAction) (alreadyDone bool, callback func(error), err error)
	WaitForStateChange(ctx context.Context, sandboxID string) error
	Reserve(sandboxID string, teamID uuid.UUID, limit int64) (func(), error)
}
