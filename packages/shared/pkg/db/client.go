package db

import (
	"fmt"
	"os"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/XSAM/otelsql"
	_ "github.com/lib/pq" //nolint:blank-imports
	semconv "go.opentelemetry.io/otel/semconv/v1.28.0"

	"github.com/e2b-dev/infra/packages/shared/pkg/models"
)

// Deprecated: use db package instead
type DB struct {
	Client *models.Client
}

// Deprecated: use db package instead
func NewClient(maxConns, maxIdle int) (*DB, error) {
	databaseURL := os.Getenv("POSTGRES_CONNECTION_STRING")
	if databaseURL == "" {
		return nil, fmt.Errorf("database URL is empty")
	}

	db, err := otelsql.Open(dialect.Postgres, databaseURL, otelsql.WithAttributes(
		semconv.DBSystemPostgreSQL,
	))
	if err != nil {
		return nil, fmt.Errorf("failed to open db: %w", err)
	}

	if err = otelsql.RegisterDBStatsMetrics(db, otelsql.WithAttributes(semconv.DBSystemPostgreSQL)); err != nil {
		return nil, fmt.Errorf("failed to register db stats metrics: %w", err)
	}

	// Get the underlying sql.DB object of the driver.
	db.SetMaxOpenConns(maxConns)
	db.SetMaxIdleConns(maxIdle)
	db.SetConnMaxLifetime(time.Minute * 30)

	drv := entsql.OpenDB(dialect.Postgres, db)

	client := models.NewClient(models.Driver(drv))

	return &DB{Client: client}, nil
}

func (db *DB) Close() error {
	return db.Client.Close()
}
