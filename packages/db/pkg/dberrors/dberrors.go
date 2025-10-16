package dberrors

import (
	"database/sql"
	"errors"
)

func IsNotFoundError(err error) bool {
	return errors.Is(err, sql.ErrNoRows)
}
