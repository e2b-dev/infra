package db

import (
	"database/sql"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	_ "github.com/lib/pq"

	"github.com/e2b-dev/infra/packages/shared/pkg/models"
)

type DB struct {
	Client *models.Client
}

func NewClient(conn *sql.DB) *DB {
	drv := entsql.OpenDB(dialect.Postgres, conn)

	client := models.NewClient(models.Driver(drv))

	return &DB{Client: client}
}

func (db *DB) Close() error {
	return db.Client.Close()
}
