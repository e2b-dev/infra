package testutils

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	dbClient, connPool, err := pool.New(t.Context(), connStr)
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

// runDatabaseMigrations executes all required database migrations
func runDatabaseMigrations(t *testing.T, connStr string) {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "git", "rev-parse", "--show-toplevel")
	output, err := cmd.Output()
	require.NoError(t, err, "Failed to find git root")

	repoRoot := strings.TrimSpace(string(output))
	dbPath := filepath.Join(repoRoot, "packages", "db")

	cmd = exec.CommandContext(t.Context(), "go", "tool", "goose", "-table", "_migrations", "-dir", "migrations", "postgres", "up")
	cmd.Env = append(os.Environ(), "GOOSE_DBSTRING="+connStr)
	cmd.Dir = dbPath

	output, err = cmd.CombinedOutput()
	require.NoError(t, err, "Migration failed: %s", string(output))
}
