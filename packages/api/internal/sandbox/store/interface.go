package store

import (
	"context"

	"github.com/google/uuid"
)

type Backend interface {
	Add(ctx context.Context, sandbox *Sandbox, newlyCreated bool) error

	Exists(ctx context.Context, instanceID string) bool
	Get(ctx context.Context, instanceID string, includeEvicting bool) (*Sandbox, error)

	Update(ctx context.Context, sandbox *Sandbox) error

	Remove(ctx context.Context, instanceID string, removeType RemoveType) error
	MarkRemoving(ctx context.Context, sandboxID string, removeType RemoveType) (*Sandbox, error)
	WaitForStop(ctx context.Context, sandboxID string) error

	Items(ctx context.Context, teamID *uuid.UUID) []*Sandbox
	ExpiredItems(ctx context.Context) []*Sandbox
	ItemsByState(ctx context.Context, teamID *uuid.UUID, states []State) map[State][]*Sandbox
	Len(ctx context.Context, teamID *uuid.UUID) int

	Reserve(ctx context.Context, sandboxID string, team uuid.UUID, limit int64) (release func(), err error)
}
