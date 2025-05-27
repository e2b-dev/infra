package db

import (
	"fmt"
	"os"
	"time"

	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/sql"
	_ "github.com/lib/pq"

	"github.com/e2b-dev/infra/packages/shared/pkg/models"
)

type DB struct {
	Client *models.Client
}

var databaseURL = os.Getenv("POSTGRES_CONNECTION_STRING")

func NewClient(maxConns, maxIdle int) (*DB, error) {
	if databaseURL == "" {
		return nil, fmt.Errorf("database URL is empty")
	}

	drv, err := sql.Open(dialect.Postgres, databaseURL)
	if err != nil {
		return nil, err
	}

	// Get the underlying sql.DB object of the driver.
	db := drv.DB()
	db.SetMaxOpenConns(maxConns)
	db.SetMaxIdleConns(maxIdle)
	db.SetConnMaxLifetime(time.Minute * 30)

	client := models.NewClient(models.Driver(drv))

	return &DB{Client: client}, nil
}

func (db *DB) Close() error {
	return db.Client.Close()
}
