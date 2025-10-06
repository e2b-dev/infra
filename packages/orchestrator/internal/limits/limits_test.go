package limits

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

func TestConcurrentAcquire(t *testing.T) {
	tempDir := t.TempDir()
	tempDBPath := filepath.Join(tempDir, "limits.db")
	query := map[string]string{
		"_journal_mode": "WAL",
		"_busy_timeout": "500",
		"_txlock":       "deferred",
		"_synchronous":  "NORMAL",
	}
	connectionString := buildQueryString(tempDBPath, query)

	db, err := sql.Open("sqlite", connectionString)
	require.NoError(t, err)
	t.Cleanup(func() {
		err := db.Close()
		assert.NoError(t, err)
	})

	err = Migrate(t.Context(), db)
	require.NoError(t, err)

	l := New(db)

	var wg sync.WaitGroup
	var timeouts atomic.Int32
	var successes atomic.Int32
	var unknownErrs atomic.Int32

	for range 10 {
		wg.Add(1)

		go func() {
			defer wg.Done()

			err := l.TryAcquire(t.Context(), "test", 5)
			if err == nil {
				successes.Add(1)
			} else if errors.Is(err, ErrTimeout) {
				timeouts.Add(1)
			} else {
				unknownErrs.Add(1)
			}
		}()
	}

	wg.Wait()

	assert.Equal(t, int32(5), timeouts.Load(), "should have zero errors")
	assert.Equal(t, int32(5), successes.Load(), "should have 5 successes")
	assert.Equal(t, int32(0), unknownErrs.Load(), "should have 5 timeouts")
}

func buildQueryString(path string, query map[string]string) string {
	var parts []string
	for k, v := range query {
		parts = append(parts, fmt.Sprintf("%s=%s", k, v))
	}
	return fmt.Sprintf("%s?%s", path, strings.Join(parts, "&"))
}
