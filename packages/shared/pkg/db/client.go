package db

import (
	"database/sql"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	_ "github.com/lib/pq"

	"github.com/e2b-dev/infra/packages/shared/pkg/models"
)

// Deprecated: use db package instead
type DB struct {
	Client *models.Client
}

// Deprecated: use db package instead
func NewClient(db *sql.DB) (*DB, error) {
	drv := entsql.OpenDB(dialect.Postgres, db)

	client := models.NewClient(models.Driver(drv))

	return &DB{Client: client}, nil
}

func (db *DB) Close() error {
	return db.Client.Close()
}
