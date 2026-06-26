package utils

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/api/internal/api"
)

func newPaginatedSandbox(id string, startedAt time.Time) PaginatedSandbox {
	return PaginatedSandbox{
		ListedSandbox: api.ListedSandbox{
			SandboxID: id,
			StartedAt: startedAt,
		},
		PaginationTimestamp: startedAt,
	}
}

func sandboxIDs(sandboxes []PaginatedSandbox) []string {
	ids := make([]string, len(sandboxes))
	for i, s := range sandboxes {
		ids[i] = s.SandboxID
	}

	return ids
}

func TestSortPaginatedSandboxes(t *testing.T) {
	t.Parallel()

	t0 := time.Date(2024, 1, 1, 10, 0, 0, 0, time.UTC)
	t1 := time.Date(2024, 1, 1, 11, 0, 0, 0, time.UTC)

	// Two sandboxes share t1 to exercise the SandboxID tie-break.
	build := func() []PaginatedSandbox {
		return []PaginatedSandbox{
			newPaginatedSandbox("b", t1),
			newPaginatedSandbox("a", t1),
			newPaginatedSandbox("c", t0),
		}
	}

	t.Run("descending: started_at desc, sandbox_id asc", func(t *testing.T) {
		t.Parallel()

		sandboxes := build()
		SortPaginatedSandboxes(sandboxes, SortDesc)
		assert.Equal(t, []string{"a", "b", "c"}, sandboxIDs(sandboxes))
	})

	t.Run("ascending: started_at asc, sandbox_id desc", func(t *testing.T) {
		t.Parallel()

		sandboxes := build()
		SortPaginatedSandboxes(sandboxes, SortAsc)
		assert.Equal(t, []string{"c", "b", "a"}, sandboxIDs(sandboxes))
	})

	t.Run("Desc wrapper matches descending", func(t *testing.T) {
		t.Parallel()

		sandboxes := build()
		SortPaginatedSandboxesDesc(sandboxes)
		assert.Equal(t, []string{"a", "b", "c"}, sandboxIDs(sandboxes))
	})
}

func TestFilterBasedOnCursor(t *testing.T) {
	t.Parallel()

	t0 := time.Date(2024, 1, 1, 10, 0, 0, 0, time.UTC)
	t1 := time.Date(2024, 1, 1, 11, 0, 0, 0, time.UTC)
	t2 := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)

	sandboxes := []PaginatedSandbox{
		newPaginatedSandbox("older", t0),
		newPaginatedSandbox("cursor-a", t1),
		newPaginatedSandbox("cursor-m", t1),
		newPaginatedSandbox("cursor-z", t1),
		newPaginatedSandbox("newer", t2),
	}

	t.Run("descending keeps older and equal-time greater id", func(t *testing.T) {
		t.Parallel()

		// Cursor at (t1, "cursor-m"): next page is everything strictly "after" it
		// in started_at DESC, sandbox_id ASC order.
		got := FilterBasedOnCursor(sandboxes, t1, "cursor-m", SortDesc)
		assert.ElementsMatch(t, []string{"cursor-z", "older"}, sandboxIDs(got))
	})

	t.Run("ascending keeps newer and equal-time smaller id", func(t *testing.T) {
		t.Parallel()

		// Cursor at (t1, "cursor-m"): next page is everything strictly "after" it
		// in started_at ASC, sandbox_id DESC order.
		got := FilterBasedOnCursor(sandboxes, t1, "cursor-m", SortAsc)
		assert.ElementsMatch(t, []string{"cursor-a", "newer"}, sandboxIDs(got))
	})
}

func TestParseFilters(t *testing.T) {
	t.Parallel()
	t.Run("happy path", func(t *testing.T) {
		t.Parallel()
		testCases := map[string]struct {
			input    string
			expected map[string]string
		}{
			"single key": {
				input: "a=b",
				expected: map[string]string{
					"a": "b",
				},
			},
			"multiple keys": {
				input: "a=b&c=d",
				expected: map[string]string{
					"a": "b",
					"c": "d",
				},
			},
		}

		for name, testCase := range testCases {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				actual, err := parseFilters(testCase.input)
				require.NoError(t, err)
				assert.Equal(t, testCase.expected, actual)
			})
		}
	})

	t.Run("sad path", func(t *testing.T) {
		t.Parallel()

		testCases := map[string]struct {
			input  string
			errMsg string
		}{
			"empty": {
				input:  "",
				errMsg: "invalid key value pair in query",
			},
			"invalid query": {
				input:  "%YY",
				errMsg: "error when unescaping query: invalid URL escape \"%YY\"",
			},
			"invalid key": {
				input:  "%25YY=a",
				errMsg: "error when unescaping key: invalid URL escape \"%YY\"",
			},
			"invalid value": {
				input:  "a=%25YY",
				errMsg: "error when unescaping value: invalid URL escape \"%YY\"",
			},
		}

		for name, testCase := range testCases {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				_, err := parseFilters(testCase.input)
				assert.EqualError(t, err, testCase.errMsg)
			})
		}
	})
}
