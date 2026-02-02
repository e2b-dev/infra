package authdb

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/lib/pq" //nolint:blank-imports

	"github.com/e2b-dev/infra/packages/db/pkg/auth/queries"
	"github.com/e2b-dev/infra/packages/db/pkg/pool"
	"github.com/e2b-dev/infra/packages/db/pkg/types"
)

type Client struct {
	Read      *authqueries.Queries
	Write     *authqueries.Queries
	writeConn *pgxpool.Pool
	readConn  *pgxpool.Pool
}

func NewClient(ctx context.Context, databaseURL, replicaURL string, options ...pool.Option) (*Client, error) {
	writeClient, writePool, err := pool.New(ctx, databaseURL, options...)
	if err != nil {
		return nil, err
	}

	writeQueries := authqueries.New(writeClient)
	readPool := writePool
	readQueries := writeQueries

	if strings.TrimSpace(replicaURL) != "" {
		var readClient types.DBTX
		readClient, readPool, err = pool.New(ctx, replicaURL, options...)
		if err != nil {
			writePool.Close()

			return nil, err
		}

		readQueries = authqueries.New(readClient)
	}

	return &Client{Read: readQueries, Write: writeQueries, writeConn: writePool, readConn: readPool}, nil
}

func (db *Client) Close() error {
	db.writeConn.Close()

	if db.readConn != nil {
		db.readConn.Close()
	}

	return nil
}

// WithTx runs the given function in a transaction.
func (db *Client) WithTx(ctx context.Context) (*authqueries.Queries, pgx.Tx, error) {
	tx, err := db.writeConn.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, nil, err
	}

	return db.Write.WithTx(tx), tx, nil
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

const getTeamWithTierByID = `
SELECT t.id, t.created_at, t.is_blocked, t.name, t.tier, t.email, t.is_banned, t.blocked_reason, t.cluster_id, t.slug,
       tl.id, tl.max_length_hours, tl.concurrent_sandboxes, tl.concurrent_template_builds, tl.max_vcpu, tl.max_ram_mb, tl.disk_mb
FROM "public"."teams" t
JOIN "public"."team_limits" tl ON tl.id = t.id
WHERE t.id = $1
`

func (db *Client) GetTeamWithLimitsByID(ctx context.Context, teamID uuid.UUID) (authqueries.Team, authqueries.TeamLimit, error) {
	row := db.readConn.QueryRow(ctx, getTeamWithTierByID, teamID)

	var team authqueries.Team
	var limit authqueries.TeamLimit

	err := row.Scan(
		&team.ID,
		&team.CreatedAt,
		&team.IsBlocked,
		&team.Name,
		&team.Tier,
		&team.Email,
		&team.IsBanned,
		&team.BlockedReason,
		&team.ClusterID,
		&team.Slug,
		&limit.ID,
		&limit.MaxLengthHours,
		&limit.ConcurrentSandboxes,
		&limit.ConcurrentTemplateBuilds,
		&limit.MaxVcpu,
		&limit.MaxRamMb,
		&limit.DiskMb,
	)
	if err != nil {
		return authqueries.Team{}, authqueries.TeamLimit{}, err
	}

	return team, limit, nil
}
