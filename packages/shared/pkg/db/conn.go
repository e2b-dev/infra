package db

import (
	"database/sql"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
)

func Open(pool *pgxpool.Pool) *sql.DB {
	connector := stdlib.GetPoolConnector(pool)
	db := sql.OpenDB(connector)

	db.SetMaxIdleConns(0) // let the pool manage the number of connections
	db.SetConnMaxLifetime(time.Minute * 30)

	return db
}
