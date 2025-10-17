package client

import (
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/lib/pq"

	database "github.com/e2b-dev/infra/packages/db/queries"
)

type Client struct {
	*database.Queries
}

func NewClient(pool *pgxpool.Pool) *Client {
	queries := database.New(pool)

	return &Client{Queries: queries}
}
