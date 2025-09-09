package store

import (
	"context"
)

type RemoveType string

const (
	RemoveTypePause RemoveType = "pause"
	RemoveTypeKill  RemoveType = "kill"
)

type (
	InsertCallback func(ctx context.Context, sbx *Sandbox, created bool)
	RemoveCallback func(ctx context.Context, sbx *Sandbox, removeType RemoveType)
)

type Store struct {
	backend Backend

	// If the callback isn't very simple, consider running it in a goroutine to prevent blocking the main flow
	insertCallbacks      []InsertCallback
	insertAsyncCallbacks []InsertCallback

	removeSandbox        func(ctx context.Context, sbx *Sandbox, removeType RemoveType) error
	removeAsyncCallbacks []RemoveCallback
}

func New(
	backend Backend,
	removeSandbox func(ctx context.Context, sbx *Sandbox, removeType RemoveType) error,
	insertCallbacks []InsertCallback,
	insertAsyncCallbacks []InsertCallback,
	removeAsyncCallbacks []RemoveCallback,
) *Store {
	return &Store{
		backend: backend,

		removeSandbox:        removeSandbox,
		removeAsyncCallbacks: removeAsyncCallbacks,

		insertCallbacks:      insertCallbacks,
		insertAsyncCallbacks: insertAsyncCallbacks,
	}
}
