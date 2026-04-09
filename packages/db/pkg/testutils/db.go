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

var (
	oneDB  *Database
	dblock sync.RWMutex
)

// SetupDatabase creates a fresh PostgreSQL container with migrations applied
func SetupDatabase(t *testing.T) *Database {
	t.Helper()

	ctx := context.WithoutCancel(t.Context())

	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// cheap lookup
	dblock.RLock()
	if oneDB != nil {
		dblock.RUnlock()

		return oneDB
	}
	dblock.RUnlock()

	// expensive lookup
	dblock.Lock()
	defer dblock.Unlock()

	if oneDB != nil {
		return oneDB
	}

	// lookup failed, create new

	// Start PostgreSQL container
	container, err := postgres.Run(
		ctx,
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

	connStr, err := container.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err, "Failed to get connection string")

	// Setup environment and run migrations
	runDatabaseMigrations(t, connStr)

	// create test queries client
	dbClient, _, err := pool.New(ctx, connStr, "tests")
	require.NoError(t, err)
	testQueries := queries.New(dbClient)

	// Create app db client
	sqlcClient, err := db.NewClient(ctx, connStr)
	require.NoError(t, err, "Failed to create sqlc client")

	// Create the auth db client
	authDb, err := authdb.NewClient(ctx, connStr, connStr)
	require.NoError(t, err, "Failed to create auth db client")

	oneDB = &Database{
		SqlcClient:  sqlcClient,
		AuthDb:      authDb,
		TestQueries: testQueries,
	}

	return oneDB
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

	gooseDB, err := goose.OpenDBWithDriver("pgx", connStr)
	require.NoError(t, err)
	t.Cleanup(func() {
		err := gooseDB.Close()
		assert.NoError(t, err)
	})

	// run the db migration
	err = goose.RunWithOptionsContext(
		t.Context(),
		"up",
		gooseDB,
		filepath.Join(repoRoot, "packages", "db", "migrations"),
		nil,
	)
	require.NoError(t, err)
}
