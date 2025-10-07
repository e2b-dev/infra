package limits

import (
	"errors"
	"math/rand"
	"time"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

var ErrRetry = errors.New("retry")

func retryBusy(retries int64, delay time.Duration, fn func() error) error {
	var sqliteError *sqlite.Error

	sleep := func() {
		time.Sleep(time.Duration(rand.Int63n(int64(delay))))
	}

	for range retries {
		err := fn()
		if err == nil {
			return nil
		}

		if errors.Is(err, ErrRetry) {
			sleep()
			continue
		}

		if errors.As(err, &sqliteError) && sqliteError.Code() == sqlite3.SQLITE_BUSY {
			sleep()
			continue
		}

		return err
	}

	return ErrTimeout
}
