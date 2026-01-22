package authdb

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/lib/pq" //nolint:blank-imports
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/db/pkg/auth/queries"
	"github.com/e2b-dev/infra/packages/db/pkg/pool"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

type Client struct {
	Read      *authqueries.Queries
	Write     *authqueries.Queries
	ctx       context.Context
	writeConn *pgxpool.Pool
	readConn  *pgxpool.Pool
}

func NewClient(ctx context.Context, databaseURL, replicaURL string, options ...pool.Option) (*Client, error) {
	if strings.TrimSpace(databaseURL) == "" {
		logger.L().Error(ctx, "POSTGRES_CONNECTION_STRING is required")

		return nil, fmt.Errorf("POSTGRES_CONNECTION_STRING is required")
	}

	writePool, err := pool.New(ctx, databaseURL, options...)
	if err != nil {
		logger.L().Error(ctx, "Unable to create write connection pool", zap.Error(err))

		return nil, err
	}

	writeClient := authqueries.New(writePool)

	var readPool *pgxpool.Pool
	readClient := writeClient

	if strings.TrimSpace(replicaURL) != "" {
		readPool, err = pool.New(ctx, replicaURL, options...)
		if err != nil {
			writePool.Close()
			logger.L().Error(ctx, "Unable to create read connection pool", zap.Error(err))

			return nil, err
		}

		readClient = authqueries.New(readPool)
	}

	return &Client{Read: readClient, Write: writeClient, ctx: ctx, writeConn: writePool, readConn: readPool}, nil
}

func (db *Client) Close() error {
	db.writeConn.Close()

	if db.readConn != nil {
		db.readConn.Close()
	}

	return nil
}

// WithTx runs the given function in a transaction.
func (db *Client) WithTx(ctx context.Context) (*Client, pgx.Tx, error) {
	tx, err := db.writeConn.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, nil, err
	}

	client := &Client{Write: db.Write.WithTx(tx), writeConn: db.writeConn, readConn: db.readConn, Read: db.Read, ctx: db.ctx}

	return client, tx, nil
}

// TestsRawSQL executes raw SQL for tests
func (db *Client) TestsRawSQL(ctx context.Context, sql string, args ...any) error {
	_, err := db.writeConn.Exec(ctx, sql, args...)

	return err
}

// TestsRawSQLQuery executes raw SQL query and processes rows with the given function
func (db *Client) TestsRawSQLQuery(ctx context.Context, sql string, processRows func(pgx.Rows) error, args ...any) error {
	rows, err := db.writeConn.Query(ctx, sql, args...)
	if err != nil {
		return err
	}
	defer rows.Close()

	return processRows(rows)
}
