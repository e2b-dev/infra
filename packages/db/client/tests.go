package client

import (
	"context"
)

func (db *Client) TestsRawSQL(ctx context.Context, sql string, args ...any) error {
	_, err := db.Pool.Exec(ctx, sql, args...)

	return err
}
