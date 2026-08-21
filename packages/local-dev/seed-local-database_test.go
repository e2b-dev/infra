package main

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/database"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

func TestRun(t *testing.T) {
	postgresContainer, err := postgres.Run(t.Context(),
		"postgres:18-alpine",
		postgres.WithDatabase("test"),
		postgres.WithUsername("postgres"),
		postgres.WithPassword("password"),
		postgres.BasicWaitStrategies(),
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		err := postgresContainer.Terminate(context.Background()) //nolint:usetesting // Cleanup runs after t.Context() is cancelled.
		assert.NoError(t, err)
	})

	connectionString, err := postgresContainer.ConnectionString(t.Context(), "sslmode=disable")
	require.NoError(t, err)
	t.Setenv("POSTGRES_CONNECTION_STRING", connectionString)

	db, err := sql.Open("pgx", connectionString)
	require.NoError(t, err)
	t.Cleanup(func() {
		err := db.Close()
		assert.NoError(t, err)
	})

	// run the db migration, through a provider carrying its own store rather
	// than goose's package-level dialect and tracking-table globals
	store, err := database.NewStore(goose.DialectPostgres, "_migrations")
	require.NoError(t, err)

	provider, err := goose.NewProvider(
		"", // Has to be empty when using a custom store
		db,
		os.DirFS(filepath.Join("..", "db", "migrations")),
		goose.WithStore(store),
	)
	require.NoError(t, err)

	_, err = provider.Up(t.Context())
	require.NoError(t, err)

	// run the seed script
	err = run(t.Context())
	require.NoError(t, err)
}
