package main

import (
	"context"
	"path/filepath"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

func init() {
	goose.SetTableName("_migrations")
}

func TestRun(t *testing.T) {
	postgresContainer, err := postgres.Run(t.Context(),
		"postgres:16-alpine",
		postgres.WithDatabase("test"),
		postgres.WithUsername("postgres"),
		postgres.WithPassword("password"),
		postgres.BasicWaitStrategies(),
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		err := postgresContainer.Terminate(context.Background())
		assert.NoError(t, err)
	})

	connectionString, err := postgresContainer.ConnectionString(t.Context(), "sslmode=disable")
	require.NoError(t, err)
	t.Setenv("POSTGRES_CONNECTION_STRING", connectionString)

	db, err := goose.OpenDBWithDriver("pgx", connectionString)
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
		filepath.Join("..", "db", "migrations"),
		nil,
	)
	require.NoError(t, err)

	// run the seed script
	err = run(t.Context())
	require.NoError(t, err)
}
