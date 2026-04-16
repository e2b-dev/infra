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
	AuthDb      *authdb.Client
	SupabaseDB  *supabasedb.Client
	TestQueries *queries.Queries
	connStr     string
}

type container struct {
	db   *Database
	lock sync.Mutex
}

var (
	databases  sync.Map
	globalLock sync.Mutex
)

func SetupDatabase(t *testing.T) *Database {
	t.Helper()

	db := SetupNamedDatabase(t, testDatabaseName)

	// Setup environment and run migrations
	db.ApplyMigrations(t, filepath.Join("packages", "db", "migrations"))

	return db
}

// SetupDatabase creates a fresh PostgreSQL container with migrations applied
func SetupNamedDatabase(t *testing.T, name string) *Database {
	t.Helper()

	ctx := context.WithoutCancel(t.Context())

	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	globalLock.Lock()
	var containerPtr *container
	blob, ok := databases.Load(name)
	if ok {
		containerPtr, ok = blob.(*container)
	}
	if !ok || containerPtr == nil {
		containerPtr = &container{}
		databases.Store(name, containerPtr)
	}
	containerPtr.lock.Lock()
	defer containerPtr.lock.Unlock()
	globalLock.Unlock()

	if containerPtr.db != nil {
		return containerPtr.db
	}

	// lookup failed, create new

	// Start PostgreSQL container
	c, err := postgres.Run(
		ctx,
		testPostgresImage,
		postgres.WithDatabase(name),
		postgres.WithUsername(testUsername),
		postgres.WithPassword(testPassword),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second),
		),
	)
	require.NoError(t, err, "Failed to start postgres container")

	connStr, err := c.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err, "Failed to get connection string")

	// create test queries client
	dbClient, _, err := pool.New(ctx, connStr, "tests")
	require.NoError(t, err)
	testQueries := queries.New(dbClient)

	// Create app db client
	sqlcClient, err := db.NewClient(ctx, connStr)
	require.NoError(t, err, "Failed to create sqlc client")

	// Create the auth db client
	authDB, err := authdb.NewClient(ctx, connStr, connStr)
	require.NoError(t, err, "Failed to create auth db client")

	supabaseDB, err := supabasedb.NewClient(t.Context(), connStr)
	require.NoError(t, err, "Failed to create supabase db client")

	containerPtr.db = &Database{
		SqlcClient:  sqlcClient,
		AuthDb:      authDB,
		SupabaseDB:  supabaseDB,
		TestQueries: testQueries,
		connStr:     connStr,
	}

	return containerPtr.db
}

func (db *Database) ConnStr() string {
	return db.connStr
}

// runDatabaseMigrations executes all required database migrations
func (db *Database) ApplyMigrations(t *testing.T, migrationDir string) {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "git", "rev-parse", "--show-toplevel")
	output, err := cmd.Output()
	require.NoError(t, err, "Failed to find git root")
	repoRoot := strings.TrimSpace(string(output))

	sqlDB, err := goose.OpenDBWithDriver("pgx", db.connStr)
	require.NoError(t, err)

	defer func() {
		err := sqlDB.Close()
		assert.NoError(t, err)
	}()

	err = goose.RunWithOptionsContext(
		t.Context(),
		"up",
		sqlDB,
		filepath.Join(repoRoot, migrationDir),
		nil,
	)

	require.NoError(t, err)
}
