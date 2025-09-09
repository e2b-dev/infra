package store

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type Store interface {
	Add(ctx context.Context, sandbox *Sandbox, newlyCreated bool) error
	Exists(instanceID string) bool
	Get(instanceID string, includeEvicting bool) (*Sandbox, error)
	Remove(ctx context.Context, instanceID string, removeType RemoveType) error
	Items(teamID *uuid.UUID) []*Sandbox
	ExpiredItems() []*Sandbox
	ItemsByState(teamID *uuid.UUID, states []State) map[State][]*Sandbox
	Len(teamID *uuid.UUID) int
	Sync(ctx context.Context, sandboxes []*Sandbox, nodeID string)
	KeepAliveFor(sandboxID string, duration time.Duration, allowShorter bool) (*Sandbox, error)
	Reserve(sandboxID string, team uuid.UUID, limit int64) (release func(), err error)
}
