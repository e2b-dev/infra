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
	for i, sandbox := range sandboxes {
		ids[i] = sandbox.SandboxID
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

func TestFilterSandboxesOnStartedAtAndTemplate(t *testing.T) {
	t.Parallel()

	// Microsecond-aligned, matching what Postgres stores, so the lower bound is
	// inclusive at the boundary.
	startedAfter := time.Date(2024, 1, 1, 11, 0, 0, 123456000, time.UTC)
	templateID := "template-a"

	sandboxes := []PaginatedSandbox{
		newPaginatedSandbox("old-template-a", startedAfter.Add(-time.Hour)),
		newPaginatedSandbox("at-bound-template-a", startedAfter),
		newPaginatedSandbox("new-template-b", startedAfter.Add(time.Hour)),
	}
	sandboxes[0].TemplateID = templateID
	sandboxes[1].TemplateID = templateID
	sandboxes[2].TemplateID = "template-b"

	filtered := FilterSandboxesOnStartedAtAndTemplate(sandboxes, startedAfter, &templateID)

	require.Len(t, filtered, 1)
	assert.Equal(t, "at-bound-template-a", filtered[0].SandboxID)
}

// TestFilterSandboxesOnStartedAtAndTemplate_BoundUsesPaginationKey pins which field
// the filter compares: PaginationTimestamp, the microsecond-aligned keyset value, not
// the nanosecond StartedAt sitting next to it. For any bound already on the microsecond
// grid the two are indistinguishable (trunc(x) < B iff x < B when B is a whole
// microsecond), so only the off-grid case below discriminates -- it is what fails if
// the comparison ever moves to StartedAt. Callers on the API path truncate the bound
// (parseSandboxListStartedAfter), so the on-grid cases are the production behavior and
// the off-grid one guards this function's own contract for any caller that does not.
func TestFilterSandboxesOnStartedAtAndTemplate_BoundUsesPaginationKey(t *testing.T) {
	t.Parallel()

	aligned := time.Date(2024, 1, 1, 11, 0, 0, 123456000, time.UTC)

	sbx := newPaginatedSandbox("sbx", aligned.Add(600*time.Nanosecond))
	sbx.PaginationTimestamp = sbx.StartedAt.Truncate(time.Microsecond)
	require.Equal(t, aligned, sbx.PaginationTimestamp)

	// An off-grid bound is the discriminating case: the keyset value (aligned) is
	// below it, the nanosecond StartedAt (aligned+600ns) is not, so this is the
	// assertion that fails if the comparison ever moves to StartedAt. Comparing the
	// keyset value keeps a retained row from sorting before the bound it satisfies.
	// Off-grid bounds do not reach here from the API path --
	// parseSandboxListStartedAfter truncates -- and that truncation is load bearing:
	// on an off-grid bound this filter drops the sandbox while the snapshot query,
	// whose bound pgx floors onto the row's own microsecond, still returns its row.
	offGrid := aligned.Add(500 * time.Nanosecond)
	assert.Empty(t, FilterSandboxesOnStartedAtAndTemplate([]PaginatedSandbox{sbx}, offGrid, nil))

	// The on-grid boundary is inclusive: a bound equal to the keyset value keeps the
	// sandbox, and Postgres receiving the same floored bound keeps its row under `>=`
	// too, so membership does not change when the sandbox pauses.
	filtered := FilterSandboxesOnStartedAtAndTemplate([]PaginatedSandbox{sbx}, aligned, nil)
	require.Len(t, filtered, 1)
	assert.Equal(t, "sbx", filtered[0].SandboxID)

	// One microsecond later both fields are below the bound and the sandbox drops.
	nextMicrosecond := aligned.Add(time.Microsecond)
	assert.Empty(t, FilterSandboxesOnStartedAtAndTemplate([]PaginatedSandbox{sbx}, nextMicrosecond, nil))
}

// TestFilterSandboxesOnStartedAtAndTemplate_DoesNotMutateInput guards the caller's
// slice: filtering in place would overwrite the retained prefix and leave stale
// duplicates in the tail, which anything still holding the unfiltered list would read.
func TestFilterSandboxesOnStartedAtAndTemplate_DoesNotMutateInput(t *testing.T) {
	t.Parallel()

	templateID := "template-a"
	startedAt := time.Date(2024, 1, 1, 11, 0, 0, 0, time.UTC)

	// The kept sandbox is second, so an in-place filter would be observable: it
	// would write "keep" over index 0 and leave the input as ["keep", "keep"].
	sandboxes := []PaginatedSandbox{
		newPaginatedSandbox("drop", startedAt),
		newPaginatedSandbox("keep", startedAt),
	}
	sandboxes[0].TemplateID = "template-b"
	sandboxes[1].TemplateID = templateID

	filtered := FilterSandboxesOnStartedAtAndTemplate(sandboxes, time.Time{}, &templateID)

	require.Len(t, filtered, 1)
	assert.Equal(t, "keep", filtered[0].SandboxID)
	assert.Equal(t, []string{"drop", "keep"}, sandboxIDs(sandboxes), "input must be left intact")
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
