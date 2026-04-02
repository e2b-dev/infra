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
	AuthDb      *authdb.Client
	TestQueries *queries.Queries
}

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
	authDb, err := authdb.NewClient(t.Context(), connStr, connStr)
	require.NoError(t, err, "Failed to create auth db client")
	t.Cleanup(func() {
		err := authDb.Close()
		assert.NoError(t, err)
	})

	return &Database{
		SqlcClient:  sqlcClient,
		AuthDb:      authDb,
		TestQueries: testQueries,
	}
}

// gooseMu serializes goose operations across parallel tests.
// goose.OpenDBWithDriver calls goose.SetDialect which writes to package-level
// globals (dialect, store) without synchronization. Concurrent test goroutines
// race on these globals, triggering the race detector on ARM64.
var gooseMu sync.Mutex

// runDatabaseMigrations executes all required database migrations
func runDatabaseMigrations(t *testing.T, connStr string) {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "git", "rev-parse", "--show-toplevel")
	output, err := cmd.Output()
	require.NoError(t, err, "Failed to find git root")
	repoRoot := strings.TrimSpace(string(output))

	gooseMu.Lock()
	defer gooseMu.Unlock()

	db, err := goose.OpenDBWithDriver("pgx", connStr)
	require.NoError(t, err)
	t.Cleanup(func() {
		err := db.Close()
		assert.NoError(t, err)
	})

	// run the db migration
	err = goose.RunWithOptionsContext(
		t.Context(),
		"up",
		db,
		filepath.Join(repoRoot, "packages", "db", "migrations"),
		nil,
	)
	require.NoError(t, err)
}
