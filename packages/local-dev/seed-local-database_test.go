package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

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

	// run the db migration
	cmd := exec.CommandContext(t.Context(), "go", "tool", "goose", "-table", "_migrations", "-dir", "migrations", "postgres", "up")
	cmd.Env = append(
		os.Environ(),
		"GOOSE_DBSTRING="+connectionString,
	)
	cmd.Dir = filepath.Join("..", "db")
	result, err := cmd.CombinedOutput()
	require.NoError(t, err, string(result))

	// run the seed script
	err = run(t.Context())
	require.NoError(t, err)
}
