package dberrors

import (
	"database/sql"
	"errors"

	"github.com/jackc/pgx/v5"
)

func IsNotFoundError(err error) bool {
	return errors.Is(err, sql.ErrNoRows) || errors.Is(err, pgx.ErrNoRows)
}
