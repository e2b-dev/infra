package testutils

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // this allows goose to function
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	db "github.com/e2b-dev/infra/packages/db/client"
	authdb "github.com/e2b-dev/infra/packages/db/pkg/auth"
	"github.com/e2b-dev/infra/packages/db/pkg/pool"
	supabasedb "github.com/e2b-dev/infra/packages/db/pkg/supabase"
	"github.com/e2b-dev/infra/packages/db/pkg/testutils/queries"
)

const (
	testPostgresImage = "postgres:16-alpine"
	testDatabaseName  = "test_db"
	testUsername      = "postgres"
	testPassword      = "test_password"
)

func init() {
	goose.SetTableName("_migrations")
}

// Database encapsulates the test database container and clients
type Database struct {
	SqlcClient  *db.Client
	AuthDB      *authdb.Client
	SupabaseDB  *supabasedb.Client
	TestQueries *queries.Queries
	connStr     string
}

// gooseMu serializes goose operations across parallel tests.
// goose.OpenDBWithDriver calls goose.SetDialect which writes to package-level
// globals (dialect, store) without synchronization. Concurrent test goroutines
// race on these globals, triggering the race detector on ARM64.
var gooseMu sync.Mutex

// SetupDatabase creates a fresh PostgreSQL container with migrations applied
func SetupDatabase(t *testing.T) *Database {
	t.Helper()

	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Start PostgreSQL container
	container, err := postgres.Run(
		t.Context(),
		testPostgresImage,
		postgres.WithDatabase(testDatabaseName),
		postgres.WithUsername(testUsername),
		postgres.WithPassword(testPassword),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second),
		),
	)
	require.NoError(t, err, "Failed to start postgres container")
	t.Cleanup(func() {
		ctx := t.Context()
		ctx = context.WithoutCancel(ctx)
		err := container.Terminate(ctx)
		assert.NoError(t, err)
	})

	connStr, err := container.ConnectionString(t.Context(), "sslmode=disable")
	require.NoError(t, err, "Failed to get connection string")

	// Setup environment and run migrations
	runDatabaseMigrations(t, connStr)

	// create test queries client
	dbClient, connPool, err := pool.New(t.Context(), connStr, "tests")
	require.NoError(t, err)
	t.Cleanup(func() {
		connPool.Close()
	})
	testQueries := queries.New(dbClient)

	// Create app db client
	sqlcClient, err := db.NewClient(t.Context(), connStr)
	require.NoError(t, err, "Failed to create sqlc client")
	t.Cleanup(func() {
		err := sqlcClient.Close()
		assert.NoError(t, err)
	})

	// Create the auth db client
	authDB, err := authdb.NewClient(t.Context(), connStr, connStr)
	require.NoError(t, err, "Failed to create auth db client")
	t.Cleanup(func() {
		err := authDB.Close()
		assert.NoError(t, err)
	})

	supabaseDB, err := supabasedb.NewClient(t.Context(), connStr)
	require.NoError(t, err, "Failed to create supabase db client")
	t.Cleanup(func() {
		err := supabaseDB.Close()
		assert.NoError(t, err)
	})

	return &Database{
		SqlcClient:  sqlcClient,
		AuthDB:      authDB,
		SupabaseDB:  supabaseDB,
		TestQueries: testQueries,
		connStr:     connStr,
	}
}

func (db *Database) ApplyMigrations(t *testing.T, migrationDirs ...string) {
	t.Helper()

	db.applyGooseMigrations(t, 0, migrationDirs...)
}

func (db *Database) ApplyMigrationsUpTo(t *testing.T, version int64, migrationDirs ...string) {
	t.Helper()

	// This is only used for staged bootstrap flows that must interleave
	// third-party migrations with goose-managed SQL migrations.
	db.applyGooseMigrations(t, version, migrationDirs...)
}

func (db *Database) ConnStr() string {
	return db.connStr
}

func (db *Database) applyGooseMigrations(t *testing.T, upToVersion int64, migrationDirs ...string) {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "git", "rev-parse", "--show-toplevel")
	output, err := cmd.Output()
	require.NoError(t, err, "Failed to find git root")
	repoRoot := strings.TrimSpace(string(output))

	gooseMu.Lock()
	defer gooseMu.Unlock()

	sqlDB, err := goose.OpenDBWithDriver("pgx", db.connStr)
	require.NoError(t, err)
	t.Cleanup(func() {
		err := sqlDB.Close()
		assert.NoError(t, err)
	})

	for _, migrationsDir := range migrationDirs {
		if upToVersion > 0 {
			err = goose.UpToContext(
				t.Context(),
				sqlDB,
				filepath.Join(repoRoot, migrationsDir),
				upToVersion,
			)
		} else {
			err = goose.RunWithOptionsContext(
				t.Context(),
				"up",
				sqlDB,
				filepath.Join(repoRoot, migrationsDir),
				nil,
			)
		}

		require.NoError(t, err)
	}
}

// runDatabaseMigrations executes all required database migrations
func runDatabaseMigrations(t *testing.T, connStr string) {
	t.Helper()

	db := &Database{connStr: connStr}
	db.ApplyMigrations(t, filepath.Join("packages", "db", "migrations"))
}
