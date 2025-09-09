package store

import (
	"context"

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
}
