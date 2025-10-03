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
	TeamID      *uuid.UUID
	States      []State
	OnlyExpired bool
}

func NewItemsFilter() *ItemsFilter {
	// Defaults to prevent accidental full scans
	return &ItemsFilter{
		States:      nil,
		TeamID:      &uuid.Nil,
		OnlyExpired: false,
	}
}

type Store interface {
	Reserve(sandboxID string, teamID uuid.UUID, limit int64) (func(), error)
	Add(ctx context.Context, sandbox Sandbox, newlyCreated bool)
	Get(sandboxID string, includeEvicting bool) (Sandbox, error)
	Remove(sandboxID string)

	Items(options ...ItemsOption) []Sandbox

	Update(sandboxID string, updateFunc func(sandbox Sandbox) (Sandbox, error)) (Sandbox, error)
	StartRemoving(ctx context.Context, sandboxID string, stateAction StateAction) (alreadyDone bool, callback func(error), err error)
	WaitForStateChange(ctx context.Context, sandboxID string) error
}

func WithTeamID(teamID uuid.UUID) ItemsOption {
	return func(f *ItemsFilter) {
		f.TeamID = &teamID
	}
}

func WithAllTeams() ItemsOption {
	return func(f *ItemsFilter) {
		f.TeamID = nil
	}
}

func WithState(state State) ItemsOption {
	return func(f *ItemsFilter) {
		f.States = []State{state}
	}
}

func WithStates(states ...State) ItemsOption {
	return func(f *ItemsFilter) {
		if len(states) == 0 {
			f.States = nil
			return
		}

		f.States = states
	}
}

func WithOnlyExpired(isExpired bool) ItemsOption {
	return func(f *ItemsFilter) {
		f.OnlyExpired = isExpired
	}
}
