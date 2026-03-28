package supabaseauthusersync

import (
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/db/pkg/testutils"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

func setupTestDB(t *testing.T) *testutils.Database {
	t.Helper()

	db := testutils.SetupDatabase(t)

	repoRoot := gitRoot(t)
	migrationSQL := readFile(t, filepath.Join(
		repoRoot,
		"packages", "db", "pkg", "dashboard", "migrations",
		"20260328000000_dashboard_supabase_auth_user_sync_queue.sql",
	))

	upSQL := extractGooseUp(migrationSQL)
	err := db.AuthDb.TestsRawSQL(t.Context(), upSQL)
	require.NoError(t, err, "failed to apply dashboard auth sync migration")

	return db
}

func gitRoot(t *testing.T) string {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "git", "rev-parse", "--show-toplevel")
	output, err := cmd.Output()
	require.NoError(t, err)

	return strings.TrimSpace(string(output))
}

func readFile(t *testing.T, path string) string {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "cat", path)
	output, err := cmd.Output()
	require.NoError(t, err)

	return string(output)
}

func extractGooseUp(sql string) string {
	parts := strings.SplitN(sql, "-- +goose Down", 2)
	up := parts[0]
	up = strings.ReplaceAll(up, "-- +goose Up", "")
	up = strings.ReplaceAll(up, "-- +goose StatementBegin", "")
	up = strings.ReplaceAll(up, "-- +goose StatementEnd", "")

	return up
}

func insertAuthUser(t *testing.T, db *testutils.Database, userID uuid.UUID, email string) {
	t.Helper()
	err := db.AuthDb.TestsRawSQL(t.Context(),
		"INSERT INTO auth.users (id, email) VALUES ($1, $2)", userID, email)
	require.NoError(t, err)
}

func updateAuthUserEmail(t *testing.T, db *testutils.Database, userID uuid.UUID, email string) {
	t.Helper()
	err := db.AuthDb.TestsRawSQL(t.Context(),
		"UPDATE auth.users SET email = $1 WHERE id = $2", email, userID)
	require.NoError(t, err)
}

func deleteAuthUser(t *testing.T, db *testutils.Database, userID uuid.UUID) {
	t.Helper()
	err := db.AuthDb.TestsRawSQL(t.Context(),
		"DELETE FROM auth.users WHERE id = $1", userID)
	require.NoError(t, err)
}

func getPublicUserEmail(t *testing.T, db *testutils.Database, userID uuid.UUID) (string, bool) {
	t.Helper()

	var email string
	var found bool

	err := db.AuthDb.TestsRawSQLQuery(t.Context(),
		"SELECT email FROM public.users WHERE id = $1",
		func(rows pgx.Rows) error {
			if rows.Next() {
				found = true
				return rows.Scan(&email)
			}
			return nil
		},
		userID,
	)
	require.NoError(t, err)

	return email, found
}

func queueDepth(t *testing.T, db *testutils.Database) int {
	t.Helper()

	var count int

	err := db.AuthDb.TestsRawSQLQuery(t.Context(),
		"SELECT count(*) FROM auth.user_sync_queue WHERE dead_lettered_at IS NULL",
		func(rows pgx.Rows) error {
			if rows.Next() {
				return rows.Scan(&count)
			}
			return nil
		},
	)
	require.NoError(t, err)

	return count
}

func TestInsertAuthUserCreatesQueueRow(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	db := setupTestDB(t)

	userID := uuid.New()
	insertAuthUser(t, db, userID, "test@example.com")

	depth := queueDepth(t, db)
	assert.Equal(t, 1, depth)
}

func TestProcessorReconciles_Insert(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	db := setupTestDB(t)
	store := NewStore(db.SqlcClient.Queries)
	l := logger.NewNopLogger()
	proc := NewProcessor(store, 5, l)

	userID := uuid.New()
	insertAuthUser(t, db, userID, "alice@example.com")

	items, err := store.ClaimBatch(t.Context(), "test-worker", 2*time.Minute, 10)
	require.NoError(t, err)
	require.Len(t, items, 1)

	proc.Process(t.Context(), items[0])

	email, found := getPublicUserEmail(t, db, userID)
	assert.True(t, found)
	assert.Equal(t, "alice@example.com", email)

	assert.Equal(t, 0, queueDepth(t, db))
}

func TestProcessorReconciles_UpdateEmail(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	db := setupTestDB(t)
	store := NewStore(db.SqlcClient.Queries)
	l := logger.NewNopLogger()
	proc := NewProcessor(store, 5, l)

	userID := uuid.New()
	insertAuthUser(t, db, userID, "old@example.com")

	items, err := store.ClaimBatch(t.Context(), "test-worker", 2*time.Minute, 10)
	require.NoError(t, err)
	proc.Process(t.Context(), items[0])

	updateAuthUserEmail(t, db, userID, "new@example.com")

	items, err = store.ClaimBatch(t.Context(), "test-worker", 2*time.Minute, 10)
	require.NoError(t, err)
	require.Len(t, items, 1)
	proc.Process(t.Context(), items[0])

	email, found := getPublicUserEmail(t, db, userID)
	assert.True(t, found)
	assert.Equal(t, "new@example.com", email)
}

func TestProcessorReconciles_Delete(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	db := setupTestDB(t)
	store := NewStore(db.SqlcClient.Queries)
	l := logger.NewNopLogger()
	proc := NewProcessor(store, 5, l)

	userID := uuid.New()
	insertAuthUser(t, db, userID, "doomed@example.com")

	items, err := store.ClaimBatch(t.Context(), "test-worker", 2*time.Minute, 10)
	require.NoError(t, err)
	proc.Process(t.Context(), items[0])

	_, found := getPublicUserEmail(t, db, userID)
	require.True(t, found)

	deleteAuthUser(t, db, userID)

	items, err = store.ClaimBatch(t.Context(), "test-worker", 2*time.Minute, 10)
	require.NoError(t, err)
	require.Len(t, items, 1)
	proc.Process(t.Context(), items[0])

	_, found = getPublicUserEmail(t, db, userID)
	assert.False(t, found)
}

func TestDuplicateQueueRowsConverge(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	db := setupTestDB(t)
	store := NewStore(db.SqlcClient.Queries)
	l := logger.NewNopLogger()
	proc := NewProcessor(store, 5, l)

	userID := uuid.New()
	insertAuthUser(t, db, userID, "dup@example.com")

	err := db.AuthDb.TestsRawSQL(t.Context(),
		"INSERT INTO auth.user_sync_queue (user_id, operation) VALUES ($1, 'upsert')",
		userID)
	require.NoError(t, err)

	items, err := store.ClaimBatch(t.Context(), "test-worker", 2*time.Minute, 10)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(items), 2)

	for _, item := range items {
		proc.Process(t.Context(), item)
	}

	email, found := getPublicUserEmail(t, db, userID)
	assert.True(t, found)
	assert.Equal(t, "dup@example.com", email)
	assert.Equal(t, 0, queueDepth(t, db))
}

func TestMultiInstanceClaimNoDoubleProcessing(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	db := setupTestDB(t)

	for i := range 10 {
		userID := uuid.New()
		insertAuthUser(t, db, userID, "user"+string(rune('a'+i))+"@example.com")
	}

	store1 := NewStore(db.SqlcClient.Queries)
	store2 := NewStore(db.SqlcClient.Queries)

	var claimed1, claimed2 atomic.Int32

	ctx := t.Context()

	items1, err := store1.ClaimBatch(ctx, "worker-1", 2*time.Minute, 10)
	require.NoError(t, err)
	claimed1.Store(int32(len(items1)))

	items2, err := store2.ClaimBatch(ctx, "worker-2", 2*time.Minute, 10)
	require.NoError(t, err)
	claimed2.Store(int32(len(items2)))

	total := claimed1.Load() + claimed2.Load()
	assert.Equal(t, int32(10), total, "all items should be claimed exactly once across both workers")

	ids := make(map[int64]bool)
	for _, item := range items1 {
		ids[item.ID] = true
	}
	for _, item := range items2 {
		assert.False(t, ids[item.ID], "item %d claimed by both workers", item.ID)
	}
}
