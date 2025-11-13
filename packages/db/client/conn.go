package client

import (
	"database/sql"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	pgxstdlib "github.com/jackc/pgx/v5/stdlib"
)

func Open(pool *pgxpool.Pool) *sql.DB {
	connector := pgxstdlib.GetPoolConnector(pool)
	db := sql.OpenDB(connector)

	db.SetMaxIdleConns(0) // let the pool manage the number of connections
	db.SetConnMaxLifetime(time.Minute * 30)

	return db
}
