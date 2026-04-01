package supabaseauthusersync

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	sqlcdb "github.com/e2b-dev/infra/packages/db/client"
	authdb "github.com/e2b-dev/infra/packages/db/pkg/auth"
	authqueries "github.com/e2b-dev/infra/packages/db/pkg/auth/queries"
	"github.com/e2b-dev/infra/packages/db/queries"
)

type QueueItem struct {
	ID           int64
	UserID       uuid.UUID
	Operation    string
	CreatedAt    time.Time
	AttemptCount int32
}

type AuthUser struct {
	ID    uuid.UUID
	Email string
}

type Store struct {
	authQueries *authqueries.Queries
	mainQueries *queries.Queries
}

var _ workerStore = (*Store)(nil)

func NewStore(authDB *authdb.Client, mainDB *sqlcdb.Client) *Store {
	return &Store{
		authQueries: authDB.Write,
		mainQueries: mainDB.Queries,
	}
}

func (s *Store) ClaimBatch(ctx context.Context, lockOwner string, lockTimeout time.Duration, batchSize int32) ([]QueueItem, error) {
	rows, err := s.authQueries.ClaimUserSyncQueueBatch(ctx, authqueries.ClaimUserSyncQueueBatchParams{
		LockOwner:   lockOwner,
		LockTimeout: durationToInterval(lockTimeout),
		BatchSize:   batchSize,
	})
	if err != nil {
		return nil, err
	}

	items := make([]QueueItem, len(rows))
	for i, r := range rows {
		items[i] = QueueItem{
			ID:           r.ID,
			UserID:       r.UserID,
			Operation:    r.Operation,
			CreatedAt:    r.CreatedAt,
			AttemptCount: r.AttemptCount,
		}
	}

	return items, nil
}

func (s *Store) AckBatch(ctx context.Context, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}

	return s.authQueries.AckUserSyncQueueItems(ctx, ids)
}

func (s *Store) Retry(ctx context.Context, id int64, backoff time.Duration, lastError string) error {
	return s.authQueries.RetryUserSyncQueueItem(ctx, authqueries.RetryUserSyncQueueItemParams{
		ID:        id,
		Backoff:   durationToInterval(backoff),
		LastError: lastError,
	})
}

func (s *Store) DeadLetter(ctx context.Context, id int64, lastError string) error {
	return s.authQueries.DeadLetterUserSyncQueueItem(ctx, authqueries.DeadLetterUserSyncQueueItemParams{
		ID:        id,
		LastError: lastError,
	})
}

func (s *Store) GetAuthUser(ctx context.Context, userID uuid.UUID) (*AuthUser, error) {
	row, err := s.authQueries.GetAuthUserByID(ctx, userID)
	if err != nil {
		return nil, err
	}

	return &AuthUser{ID: row.ID, Email: row.Email}, nil
}

func (s *Store) UpsertPublicUser(ctx context.Context, id uuid.UUID, email string) error {
	return s.mainQueries.UpsertPublicUser(ctx, queries.UpsertPublicUserParams{
		ID:    id,
		Email: email,
	})
}

func (s *Store) DeletePublicUser(ctx context.Context, id uuid.UUID) error {
	return s.mainQueries.DeletePublicUser(ctx, id)
}

func durationToInterval(d time.Duration) pgtype.Interval {
	return pgtype.Interval{
		Microseconds: d.Microseconds(),
		Valid:        true,
	}
}
