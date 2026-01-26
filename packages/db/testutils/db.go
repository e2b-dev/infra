package testutils

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	db "github.com/e2b-dev/infra/packages/db/client"
	authdb "github.com/e2b-dev/infra/packages/db/pkg/auth"
)

const (
	testPostgresImage = "postgres:16-alpine"
	testDatabaseName  = "test_db"
	testUsername      = "postgres"
	testPassword      = "test_password"
)

// Database encapsulates the test database container and clients
type Database struct {
	SqlcClient *db.Client
	AuthDb     *authdb.Client
}

// SetupDatabase creates a fresh PostgreSQL container with migrations applied
func SetupDatabase(t *testing.T) *Database {
	t.Helper()

	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Start PostgreSQL container
	container := startPostgresContainer(t)
	connStr, err := container.ConnectionString(t.Context(), "sslmode=disable")
	require.NoError(t, err, "Failed to get connection string")

	// Setup environment and run migrations
	runDatabaseMigrations(t, connStr)

	// Create database client
	sqlcClient, err := db.NewClient(t.Context(), connStr)
	require.NoError(t, err, "Failed to create sqlc client")

	authDb, err := authdb.NewClient(t.Context(), connStr, connStr)
	require.NoError(t, err, "Failed to create auth db client")

	// Register cleanup
	t.Cleanup(func() {
		_ = authDb.Close()
		cleanupTestDatabase(t, context.WithoutCancel(t.Context()), sqlcClient, container)
	})

	return &Database{
		SqlcClient: sqlcClient,
		AuthDb:     authDb,
	}
}

// startPostgresContainer initializes and starts a PostgreSQL container
func startPostgresContainer(t *testing.T) *postgres.PostgresContainer {
	t.Helper()

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

	return container
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

// cleanupTestDatabase terminates the container and restores environment
func cleanupTestDatabase(tb testing.TB, ctx context.Context, sqlcClient *db.Client, container *postgres.PostgresContainer) {
	tb.Helper()

	if sqlcClient != nil {
		err := sqlcClient.Close()
		if err != nil {
			tb.Errorf("Failed to close sqlc client: %s", err)
		}
	}

	if container != nil {
		err := container.Terminate(ctx)
		if err != nil {
			tb.Errorf("Failed to terminate container: %s", err)
		}
	}
}
