package supabaseauthusersync

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

type retryCall struct {
	id        int64
	backoff   time.Duration
	lastError string
}

type deadLetterCall struct {
	id        int64
	lastError string
}

type fakeProcessorStore struct {
	getAuthUserFn func(context.Context, uuid.UUID) (*AuthUser, error)

	deletePublicUserCalls int
	retryCalls            []retryCall
	deadLetterCalls       []deadLetterCall
}

func (s *fakeProcessorStore) Retry(_ context.Context, id int64, backoff time.Duration, lastError string) error {
	s.retryCalls = append(s.retryCalls, retryCall{
		id:        id,
		backoff:   backoff,
		lastError: lastError,
	})

	return nil
}

func (s *fakeProcessorStore) DeadLetter(_ context.Context, id int64, lastError string) error {
	s.deadLetterCalls = append(s.deadLetterCalls, deadLetterCall{
		id:        id,
		lastError: lastError,
	})

	return nil
}

func (s *fakeProcessorStore) GetAuthUser(ctx context.Context, userID uuid.UUID) (*AuthUser, error) {
	return s.getAuthUserFn(ctx, userID)
}

func (s *fakeProcessorStore) UpsertPublicUser(_ context.Context, _ uuid.UUID, _ string) error {
	return nil
}

func (s *fakeProcessorStore) DeletePublicUser(_ context.Context, _ uuid.UUID) error {
	s.deletePublicUserCalls++

	return nil
}

func TestProcessorProcessRetriesRecoveredPanic(t *testing.T) {
	t.Parallel()

	store := &fakeProcessorStore{
		getAuthUserFn: func(context.Context, uuid.UUID) (*AuthUser, error) {
			panic("boom")
		},
	}
	processor := NewProcessor(store, 3, logger.NewNopLogger())
	item := QueueItem{
		ID:           1,
		UserID:       uuid.New(),
		AttemptCount: 1,
	}

	require.NotPanics(t, func() {
		processor.process(context.Background(), item)
	})
	require.Len(t, store.retryCalls, 1)
	require.Contains(t, store.retryCalls[0].lastError, "panic while processing queue item")
	require.Empty(t, store.deadLetterCalls)
}

func TestProcessorProcessDeadLettersRecoveredPanicAtMaxAttempts(t *testing.T) {
	t.Parallel()

	store := &fakeProcessorStore{
		getAuthUserFn: func(context.Context, uuid.UUID) (*AuthUser, error) {
			panic("boom")
		},
	}
	processor := NewProcessor(store, 3, logger.NewNopLogger())
	item := QueueItem{
		ID:           1,
		UserID:       uuid.New(),
		AttemptCount: 3,
	}

	require.NotPanics(t, func() {
		processor.process(context.Background(), item)
	})
	require.Empty(t, store.retryCalls)
	require.Len(t, store.deadLetterCalls, 1)
	require.Contains(t, store.deadLetterCalls[0].lastError, "panic while processing queue item")
}

func TestProcessorProcessDeleteSkipsAuthLookup(t *testing.T) {
	t.Parallel()

	getAuthUserCalled := false
	store := &fakeProcessorStore{
		getAuthUserFn: func(context.Context, uuid.UUID) (*AuthUser, error) {
			getAuthUserCalled = true

			return nil, nil
		},
	}
	processor := NewProcessor(store, 3, logger.NewNopLogger())
	item := QueueItem{
		ID:           1,
		UserID:       uuid.New(),
		Operation:    "delete",
		AttemptCount: 1,
	}

	result := processor.process(context.Background(), item)

	require.False(t, getAuthUserCalled)
	require.Equal(t, 1, store.deletePublicUserCalls)
	require.Equal(t, processOutcomeReadyToAck, result.Outcome)
	require.Equal(t, reconcileActionDeletePublicUser, result.Action)
}
