package sandbox

import (
	"context"

	"github.com/google/uuid"
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

type Store interface {
	Reserve(sandboxID string, teamID uuid.UUID, limit int64) (func(), error)
	Add(ctx context.Context, sandbox Sandbox, newlyCreated bool)
	Get(sandboxID string) (Sandbox, error)
	Remove(sandboxID string)

	Items(teamID *uuid.UUID, states []State, options ...ItemsOption) []Sandbox

	Update(sandboxID string, updateFunc func(sandbox Sandbox) (Sandbox, error)) (Sandbox, error)
	StartRemoving(ctx context.Context, sandboxID string, stateAction StateAction) (alreadyDone bool, callback func(error), err error)
	WaitForStateChange(ctx context.Context, sandboxID string) error
}

func WithOnlyExpired(isExpired bool) ItemsOption {
	return func(f *ItemsFilter) {
		f.OnlyExpired = isExpired
	}
}
