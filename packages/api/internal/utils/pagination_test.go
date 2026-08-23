package utils

import (
	"encoding/base64"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testItem struct {
	ID        string
	Timestamp time.Time
}

func TestGenerateCursor(t *testing.T) {
	t.Parallel()
	timestamp := time.Date(2024, 1, 15, 10, 30, 45, 123456789, time.UTC)
	id := "test-id-123"

	cursor := generateCursor(timestamp, id, SortDesc)

	// Decode and verify
	decoded, err := base64.URLEncoding.DecodeString(cursor)
	require.NoError(t, err)

	expected := fmt.Sprintf("%s__%s", timestamp.Format(time.RFC3339Nano), id)
	assert.Equal(t, expected, string(decoded))
}

func TestNewPagination(t *testing.T) {
	t.Parallel()
	config := PaginationConfig{
		DefaultLimit: 10,
		MaxLimit:     100,
		DefaultID:    "default-id",
	}

	t.Run("with default limit", func(t *testing.T) {
		t.Parallel()
		p, err := NewPagination[testItem](PaginationParams{}, config)
		require.NoError(t, err)
		assert.Equal(t, int32(10), p.limit)
		assert.Equal(t, int32(11), p.QueryLimit())
		assert.Equal(t, config.DefaultID, p.CursorID())
	})

	t.Run("with custom limit", func(t *testing.T) {
		t.Parallel()
		limit := int32(25)
		p, err := NewPagination[testItem](
			PaginationParams{Limit: &limit},
			config,
		)
		require.NoError(t, err)
		assert.Equal(t, int32(25), p.limit)
		assert.Equal(t, int32(26), p.QueryLimit())
	})

	t.Run("with limit exceeding max", func(t *testing.T) {
		t.Parallel()
		limit := int32(150)
		p, err := NewPagination[testItem](
			PaginationParams{Limit: &limit},
			config,
		)
		require.NoError(t, err)
		assert.Equal(t, int32(100), p.limit)
		assert.Equal(t, int32(101), p.QueryLimit())
	})

	t.Run("with limit below minimum", func(t *testing.T) {
		t.Parallel()
		// Nothing validates the request against the spec's `minimum: 1` at
		// runtime, so a zero or negative limit reaches this constructor and must
		// be clamped before it can index results[limit-1] out of range.
		for _, limit := range []int32{0, -1, -100} {
			p, err := NewPagination[testItem](
				PaginationParams{Limit: &limit},
				config,
			)
			require.NoError(t, err)
			assert.Equal(t, int32(1), p.limit, "limit %d should clamp to the minimum", limit)
			assert.Equal(t, int32(2), p.QueryLimit())
		}
	})

	t.Run("with valid next token", func(t *testing.T) {
		t.Parallel()
		timestamp := time.Date(2024, 1, 15, 10, 30, 45, 123456789, time.UTC)
		id := "test-id-123"
		token := generateCursor(timestamp, id, SortDesc)

		p, err := NewPagination[testItem](
			PaginationParams{NextToken: &token},
			config,
		)
		require.NoError(t, err)
		// Floored to the microsecond grid the keyset values sit on; see
		// TestPagination_CursorTimeIsMicrosecondAligned.
		assert.Equal(t, timestamp.Truncate(time.Microsecond), p.CursorTime())
		assert.Equal(t, id, p.CursorID())
	})

	t.Run("with invalid next token", func(t *testing.T) {
		t.Parallel()
		invalidToken := "invalid-token"
		_, err := NewPagination[testItem](
			PaginationParams{NextToken: &invalidToken},
			config,
		)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid next token")
	})

	t.Run("with empty next token", func(t *testing.T) {
		t.Parallel()
		emptyToken := ""
		p, err := NewPagination[testItem](
			PaginationParams{NextToken: &emptyToken},
			config,
		)
		require.NoError(t, err)
		assert.Equal(t, config.DefaultID, p.CursorID())
	})

	t.Run("with nil next token", func(t *testing.T) {
		t.Parallel()
		p, err := NewPagination[testItem](
			PaginationParams{NextToken: nil},
			config,
		)
		require.NoError(t, err)
		assert.Equal(t, config.DefaultID, p.CursorID())
	})
}

func TestPagination_Limit(t *testing.T) {
	t.Parallel()
	config := PaginationConfig{
		DefaultLimit: 20,
		MaxLimit:     100,
		DefaultID:    "default-id",
	}

	p, err := NewPagination[testItem](PaginationParams{}, config)
	require.NoError(t, err)
	assert.Equal(t, int32(20), p.limit)
}

func TestPagination_QueryLimit(t *testing.T) {
	t.Parallel()
	config := PaginationConfig{
		DefaultLimit: 20,
		MaxLimit:     100,
		DefaultID:    "default-id",
	}

	p, err := NewPagination[testItem](PaginationParams{}, config)
	require.NoError(t, err)
	assert.Equal(t, int32(21), p.QueryLimit())
}

func TestPagination_Cursor(t *testing.T) {
	t.Parallel()
	config := PaginationConfig{
		DefaultLimit: 10,
		MaxLimit:     100,
		DefaultID:    "default-id",
	}

	timestamp := time.Date(2024, 1, 15, 10, 30, 45, 123456789, time.UTC)
	id := "test-id-123"
	token := generateCursor(timestamp, id, SortDesc)

	p, err := NewPagination[testItem](
		PaginationParams{NextToken: &token},
		config,
	)
	require.NoError(t, err)

	cursor := p.cursor
	assert.Equal(t, timestamp.Truncate(time.Microsecond), cursor.Time)
	assert.Equal(t, id, cursor.ID)
}

func TestPagination_CursorTime(t *testing.T) {
	t.Parallel()
	config := PaginationConfig{
		DefaultLimit: 10,
		MaxLimit:     100,
		DefaultID:    "default-id",
	}

	timestamp := time.Date(2024, 1, 15, 10, 30, 45, 123456789, time.UTC)
	id := "test-id-123"
	token := generateCursor(timestamp, id, SortDesc)

	p, err := NewPagination[testItem](
		PaginationParams{NextToken: &token},
		config,
	)
	require.NoError(t, err)

	assert.Equal(t, timestamp.Truncate(time.Microsecond), p.CursorTime())
}

// TestPagination_CursorTimeIsMicrosecondAligned pins the cursor time onto the same
// microsecond grid as the keyset values it is compared against: PaginationTimestamp in
// FilterBasedOnCursor and sandbox_started_at in the SQL predicate, both of which are
// microsecond-aligned. A nanosecond cursor time sorts strictly before the row that
// minted it, so that row is re-served on the next page. Tokens are minted from the
// keyset value now, but one minted before that change can still be in flight during a
// rollout.
func TestPagination_CursorTimeIsMicrosecondAligned(t *testing.T) {
	t.Parallel()

	aligned := time.Date(2024, 1, 15, 10, 30, 45, 123456000, time.UTC)
	config := PaginationConfig{DefaultLimit: 10, MaxLimit: 100, DefaultID: "default-id"}

	// A token carrying the nanosecond StartedAt, as the pre-rollout code minted.
	token := generateCursor(aligned.Add(789*time.Nanosecond), "sbx", SortDesc)

	p, err := NewPagination[testItem](PaginationParams{NextToken: &token}, config)
	require.NoError(t, err)

	assert.Equal(t, aligned, p.CursorTime())
	assert.Zero(t, p.CursorTime().Nanosecond()%1000)

	// The row that minted the token is now excluded rather than re-emitted.
	last := PaginatedSandbox{PaginationTimestamp: aligned}
	last.SandboxID = "sbx"
	assert.Empty(t, FilterBasedOnCursor([]PaginatedSandbox{last}, p.CursorTime(), p.CursorID(), SortDesc))
}

func TestPagination_DefaultCursorByDirection(t *testing.T) {
	t.Parallel()

	t.Run("descending defaults to now", func(t *testing.T) {
		t.Parallel()

		before := time.Now()
		p, err := NewPagination[testItem](PaginationParams{}, PaginationConfig{
			DefaultLimit: 10,
			MaxLimit:     100,
			DefaultID:    "default-id",
		})
		require.NoError(t, err)
		after := time.Now()

		assert.False(t, p.CursorTime().Before(before), "default cursor time should be ~now")
		assert.False(t, p.CursorTime().After(after), "default cursor time should be ~now")
		assert.Equal(t, "default-id", p.CursorID())
	})

	t.Run("ascending defaults to zero time", func(t *testing.T) {
		t.Parallel()

		p, err := NewPagination[testItem](PaginationParams{}, PaginationConfig{
			DefaultLimit: 10,
			MaxLimit:     100,
			DefaultID:    "default-id",
			Order:        SortAsc,
		})
		require.NoError(t, err)

		assert.True(t, p.CursorTime().IsZero(), "ascending default cursor time should be the zero time")
		assert.Equal(t, "default-id", p.CursorID())
	})
}

func TestPagination_CursorID(t *testing.T) {
	t.Parallel()
	config := PaginationConfig{
		DefaultLimit: 10,
		MaxLimit:     100,
		DefaultID:    "default-id",
	}

	timestamp := time.Date(2024, 1, 15, 10, 30, 45, 123456789, time.UTC)
	id := "test-id-123"
	token := generateCursor(timestamp, id, SortDesc)

	p, err := NewPagination[testItem](
		PaginationParams{NextToken: &token},
		config,
	)
	require.NoError(t, err)

	assert.Equal(t, id, p.CursorID())
}

func TestPagination_SetNextToken(t *testing.T) {
	t.Parallel()
	config := PaginationConfig{
		DefaultLimit: 10,
		MaxLimit:     100,
		DefaultID:    "default-id",
	}

	p, err := NewPagination[testItem](PaginationParams{}, config)
	require.NoError(t, err)

	timestamp := time.Date(2024, 1, 15, 10, 30, 45, 123456789, time.UTC)
	id := "test-id-123"
	p.setNextToken(timestamp, id)

	// Verify the token was set correctly by checking it can be parsed
	require.NotNil(t, p.nextToken)
	parsedTime, parsedID, _, err := ParseCursor(*p.nextToken)
	require.NoError(t, err)
	assert.Equal(t, timestamp, parsedTime)
	assert.Equal(t, id, parsedID)
}

func TestPagination_HasMore(t *testing.T) {
	t.Parallel()
	config := PaginationConfig{
		DefaultLimit: 10,
		MaxLimit:     100,
		DefaultID:    "default-id",
	}

	p, err := NewPagination[testItem](PaginationParams{}, config)
	require.NoError(t, err)

	t.Run("no more results", func(t *testing.T) {
		t.Parallel()
		assert.False(t, p.hasMore(5))
		assert.False(t, p.hasMore(10))
	})

	t.Run("has more results", func(t *testing.T) {
		t.Parallel()
		assert.True(t, p.hasMore(11))
		assert.True(t, p.hasMore(15))
	})
}

func TestPagination_TrimResults(t *testing.T) {
	t.Parallel()
	config := PaginationConfig{
		DefaultLimit: 5,
		MaxLimit:     100,
		DefaultID:    "default-id",
	}

	p, err := NewPagination[testItem](PaginationParams{}, config)
	require.NoError(t, err)

	t.Run("no trimming needed", func(t *testing.T) {
		t.Parallel()
		results := []testItem{
			{ID: "1", Timestamp: time.Now()},
			{ID: "2", Timestamp: time.Now()},
			{ID: "3", Timestamp: time.Now()},
		}
		trimmed := p.trimResults(results)
		assert.Len(t, trimmed, 3)
		assert.Equal(t, results, trimmed)
	})

	t.Run("trimming needed", func(t *testing.T) {
		t.Parallel()
		results := []testItem{
			{ID: "1", Timestamp: time.Now()},
			{ID: "2", Timestamp: time.Now()},
			{ID: "3", Timestamp: time.Now()},
			{ID: "4", Timestamp: time.Now()},
			{ID: "5", Timestamp: time.Now()},
			{ID: "6", Timestamp: time.Now()},
			{ID: "7", Timestamp: time.Now()},
		}
		trimmed := p.trimResults(results)
		assert.Len(t, trimmed, 5)
		assert.Equal(t, results[:5], trimmed)
	})

	t.Run("exact limit", func(t *testing.T) {
		t.Parallel()
		results := []testItem{
			{ID: "1", Timestamp: time.Now()},
			{ID: "2", Timestamp: time.Now()},
			{ID: "3", Timestamp: time.Now()},
			{ID: "4", Timestamp: time.Now()},
			{ID: "5", Timestamp: time.Now()},
		}
		trimmed := p.trimResults(results)
		assert.Len(t, trimmed, 5)
		assert.Equal(t, results, trimmed)
	})
}

func TestPagination_ProcessResults(t *testing.T) {
	t.Parallel()
	config := PaginationConfig{
		DefaultLimit: 5,
		MaxLimit:     100,
		DefaultID:    "default-id",
	}

	getTimestampAndID := func(item testItem) (time.Time, string) {
		return item.Timestamp, item.ID
	}

	t.Run("no more results", func(t *testing.T) {
		t.Parallel()
		p, err := NewPagination[testItem](PaginationParams{}, config)
		require.NoError(t, err)

		results := []testItem{
			{ID: "1", Timestamp: time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)},
			{ID: "2", Timestamp: time.Date(2024, 1, 15, 11, 0, 0, 0, time.UTC)},
			{ID: "3", Timestamp: time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)},
		}

		processed := p.processResults(results, getTimestampAndID)
		assert.Len(t, processed, 3)
		assert.Nil(t, p.nextToken) // No next token should be set
	})

	t.Run("has more results", func(t *testing.T) {
		t.Parallel()
		p, err := NewPagination[testItem](PaginationParams{}, config)
		require.NoError(t, err)

		results := []testItem{
			{ID: "1", Timestamp: time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)},
			{ID: "2", Timestamp: time.Date(2024, 1, 15, 11, 0, 0, 0, time.UTC)},
			{ID: "3", Timestamp: time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)},
			{ID: "4", Timestamp: time.Date(2024, 1, 15, 13, 0, 0, 0, time.UTC)},
			{ID: "5", Timestamp: time.Date(2024, 1, 15, 14, 0, 0, 0, time.UTC)},
			{ID: "6", Timestamp: time.Date(2024, 1, 15, 15, 0, 0, 0, time.UTC)},
		}

		processed := p.processResults(results, getTimestampAndID)
		assert.Len(t, processed, 5)
		assert.NotNil(t, p.nextToken) // Next token should be set

		// Verify next token is based on the 5th item (index 4)
		parsedTime, parsedID, _, err := ParseCursor(*p.nextToken)
		require.NoError(t, err)
		assert.Equal(t, results[4].Timestamp, parsedTime)
		assert.Equal(t, results[4].ID, parsedID)
	})

	// Regression: a below-minimum limit used to reach this code path unclamped
	// and panic on results[limit-1], turning `?limit=0` into a 500.
	t.Run("below-minimum limit does not panic", func(t *testing.T) {
		t.Parallel()
		for _, limit := range []int32{0, -5} {
			p, err := NewPagination[testItem](PaginationParams{Limit: &limit}, config)
			require.NoError(t, err)

			results := []testItem{
				{ID: "1", Timestamp: time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)},
				{ID: "2", Timestamp: time.Date(2024, 1, 15, 11, 0, 0, 0, time.UTC)},
			}

			processed := p.processResults(results, getTimestampAndID)
			assert.Len(t, processed, 1)
			assert.NotNil(t, p.nextToken)
		}
	})
}

func TestPagination_ProcessResultsWithGin(t *testing.T) {
	t.Parallel()
	config := PaginationConfig{
		DefaultLimit: 5,
		MaxLimit:     100,
		DefaultID:    "default-id",
	}

	getTimestampAndID := func(item testItem) (time.Time, string) {
		return item.Timestamp, item.ID
	}

	t.Run("sets header when has more results", func(t *testing.T) {
		t.Parallel()
		p, err := NewPagination[testItem](PaginationParams{}, config)
		require.NoError(t, err)

		results := []testItem{
			{ID: "1", Timestamp: time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)},
			{ID: "2", Timestamp: time.Date(2024, 1, 15, 11, 0, 0, 0, time.UTC)},
			{ID: "3", Timestamp: time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)},
			{ID: "4", Timestamp: time.Date(2024, 1, 15, 13, 0, 0, 0, time.UTC)},
			{ID: "5", Timestamp: time.Date(2024, 1, 15, 14, 0, 0, 0, time.UTC)},
			{ID: "6", Timestamp: time.Date(2024, 1, 15, 15, 0, 0, 0, time.UTC)},
		}

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)

		processed := p.ProcessResultsWithHeader(c, results, getTimestampAndID)
		assert.Len(t, processed, 5)
		assert.NotNil(t, p.nextToken)

		// Verify header was set
		assert.Equal(t, *p.nextToken, c.Writer.Header().Get("X-Next-Token"))
	})

	t.Run("no header when no more results", func(t *testing.T) {
		t.Parallel()
		p, err := NewPagination[testItem](PaginationParams{}, config)
		require.NoError(t, err)

		results := []testItem{
			{ID: "1", Timestamp: time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)},
			{ID: "2", Timestamp: time.Date(2024, 1, 15, 11, 0, 0, 0, time.UTC)},
			{ID: "3", Timestamp: time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)},
		}

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)

		processed := p.ProcessResultsWithHeader(c, results, getTimestampAndID)
		assert.Len(t, processed, 3)
		assert.Nil(t, p.nextToken)

		// Verify header was not set
		assert.Empty(t, c.Writer.Header().Get("X-Next-Token"))
	})
}

func TestPagination_SetHeader(t *testing.T) {
	t.Parallel()
	config := PaginationConfig{
		DefaultLimit: 10,
		MaxLimit:     100,
		DefaultID:    "default-id",
	}

	t.Run("sets header when next token exists", func(t *testing.T) {
		t.Parallel()
		p, err := NewPagination[testItem](PaginationParams{}, config)
		require.NoError(t, err)

		timestamp := time.Date(2024, 1, 15, 10, 30, 45, 123456789, time.UTC)
		id := "test-id-123"
		p.setNextToken(timestamp, id)

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)

		p.setHeader(c)
		assert.Equal(t, *p.nextToken, c.Writer.Header().Get("X-Next-Token"))
	})

	t.Run("does not set header when next token is nil", func(t *testing.T) {
		t.Parallel()
		p, err := NewPagination[testItem](PaginationParams{}, config)
		require.NoError(t, err)

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)

		p.setHeader(c)
		assert.Empty(t, c.Writer.Header().Get("X-Next-Token"))
	})
}

func TestParseCursor(t *testing.T) {
	t.Parallel()
	t.Run("valid cursor", func(t *testing.T) {
		t.Parallel()
		timestamp := time.Date(2024, 1, 15, 10, 30, 45, 123456789, time.UTC)
		id := "test-id-123"
		cursor := generateCursor(timestamp, id, SortDesc)

		parsedTime, parsedID, _, err := ParseCursor(cursor)
		require.NoError(t, err)
		assert.Equal(t, timestamp, parsedTime)
		assert.Equal(t, id, parsedID)
	})

	t.Run("invalid base64 encoding", func(t *testing.T) {
		t.Parallel()
		_, _, _, err := ParseCursor("invalid-base64!")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "error decoding cursor")
	})

	t.Run("invalid format - missing separator", func(t *testing.T) {
		t.Parallel()
		invalid := base64.URLEncoding.EncodeToString([]byte("not-valid-format"))
		_, _, _, err := ParseCursor(invalid)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid cursor format")
	})

	t.Run("invalid timestamp format", func(t *testing.T) {
		t.Parallel()
		invalid := base64.URLEncoding.EncodeToString([]byte("invalid-timestamp__test-id"))
		_, _, _, err := ParseCursor(invalid)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid timestamp format")
	})

	t.Run("two-part cursor is descending", func(t *testing.T) {
		t.Parallel()
		// Tokens minted before ordering existed carry no direction and must keep
		// resolving to the default order.
		legacy := base64.URLEncoding.EncodeToString([]byte("2024-01-15T10:30:45Z__test-id"))
		_, _, order, err := ParseCursor(legacy)
		require.NoError(t, err)
		assert.Equal(t, SortDesc, order)
	})

	t.Run("three-part cursor carries the direction", func(t *testing.T) {
		t.Parallel()
		timestamp := time.Date(2024, 1, 15, 10, 30, 45, 123456789, time.UTC)
		cursor := generateCursor(timestamp, "test-id", SortAsc)

		decoded, err := base64.URLEncoding.DecodeString(cursor)
		require.NoError(t, err)
		assert.Equal(t, fmt.Sprintf("%s__test-id__asc", timestamp.Format(time.RFC3339Nano)), string(decoded))

		parsedTime, parsedID, order, err := ParseCursor(cursor)
		require.NoError(t, err)
		assert.Equal(t, timestamp, parsedTime)
		assert.Equal(t, "test-id", parsedID)
		assert.Equal(t, SortAsc, order)
	})

	t.Run("unknown sort direction", func(t *testing.T) {
		t.Parallel()
		invalid := base64.URLEncoding.EncodeToString([]byte("2024-01-15T10:30:45Z__test-id__sideways"))
		_, _, _, err := ParseCursor(invalid)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid sort direction")
	})

	t.Run("too many parts", func(t *testing.T) {
		t.Parallel()
		invalid := base64.URLEncoding.EncodeToString([]byte("2024-01-15T10:30:45Z__test-id__asc__extra"))
		_, _, _, err := ParseCursor(invalid)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid cursor format")
	})
}

// TestPagination_RejectsCursorFromOtherDirection guards the keyset invariant: the
// two directions are exact inverses, so a token replayed under the other order
// would re-serve rows the caller already saw instead of advancing.
func TestPagination_RejectsCursorFromOtherDirection(t *testing.T) {
	t.Parallel()

	timestamp := time.Date(2024, 1, 15, 10, 30, 45, 123456789, time.UTC)
	descToken := generateCursor(timestamp, "test-id", SortDesc)
	ascToken := generateCursor(timestamp, "test-id", SortAsc)

	config := func(order SortDirection) PaginationConfig {
		return PaginationConfig{
			DefaultLimit: 10,
			MaxLimit:     100,
			DefaultID:    "default-id",
			Order:        order,
		}
	}

	t.Run("descending token on an ascending request", func(t *testing.T) {
		t.Parallel()
		_, err := NewPagination[testItem](PaginationParams{NextToken: &descToken}, config(SortAsc))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "different sort order")
	})

	t.Run("ascending token on a descending request", func(t *testing.T) {
		t.Parallel()
		_, err := NewPagination[testItem](PaginationParams{NextToken: &ascToken}, config(SortDesc))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "different sort order")
	})

	t.Run("matching directions round-trip", func(t *testing.T) {
		t.Parallel()
		for order, token := range map[SortDirection]string{SortDesc: descToken, SortAsc: ascToken} {
			p, err := NewPagination[testItem](PaginationParams{NextToken: &token}, config(order))
			require.NoError(t, err)
			assert.Equal(t, timestamp.Truncate(time.Microsecond), p.CursorTime())
			assert.Equal(t, "test-id", p.CursorID())
		}
	})
}
