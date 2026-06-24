package handlers

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/dashboard-api/internal/api"
)

func TestParseTemplatesSort(t *testing.T) {
	t.Parallel()

	createdAsc := api.GetTemplatesParamsSortCreatedAtAsc
	updatedDesc := api.GetTemplatesParamsSortUpdatedAtDesc
	bogus := api.GetTemplatesParamsSort("bogus")

	tests := []struct {
		name    string
		input   *api.GetTemplatesParamsSort
		want    templatesSort
		wantErr bool
	}{
		{"defaults to created_at_desc", nil, templatesSortCreatedAtDesc, false},
		{"created_at_asc", &createdAsc, templatesSortCreatedAtAsc, false},
		{"updated_at_desc", &updatedDesc, templatesSortUpdatedAtDesc, false},
		{"invalid", &bogus, "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := parseTemplatesSort(tt.input)
			if tt.wantErr {
				assert.Error(t, err)

				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestTemplatesCursorRoundTrip(t *testing.T) {
	t.Parallel()

	token := formatTemplatesCursor(templatesSortCreatedAtAsc, "2026-05-30T12:34:56Z", "env-abc")

	value, id, err := parseTemplatesCursor(&token, templatesSortCreatedAtAsc)
	require.NoError(t, err)
	require.NotNil(t, value)
	require.NotNil(t, id)
	assert.Equal(t, "2026-05-30T12:34:56Z", *value)
	assert.Equal(t, "env-abc", *id)
}

func TestParseTemplatesCursor(t *testing.T) {
	t.Parallel()

	empty := api.TemplatesCursor("")
	value, id, err := parseTemplatesCursor(&empty, templatesSortUpdatedAtDesc)
	require.NoError(t, err)
	assert.Nil(t, value)
	assert.Nil(t, id)

	value, id, err = parseTemplatesCursor(nil, templatesSortUpdatedAtDesc)
	require.NoError(t, err)
	assert.Nil(t, value)
	assert.Nil(t, id)

	// Sort mismatch is rejected with a dedicated error.
	mismatched := formatTemplatesCursor(templatesSortCreatedAtAsc, "x", "env-1")
	_, _, err = parseTemplatesCursor(&mismatched, templatesSortUpdatedAtDesc)
	require.ErrorIs(t, err, errTemplatesCursorSortMismatch)

	// Malformed cursors are rejected.
	malformed := api.TemplatesCursor("not-a-cursor")
	_, _, err = parseTemplatesCursor(&malformed, templatesSortUpdatedAtDesc)
	require.ErrorIs(t, err, errInvalidTemplatesCursor)

	// Empty id segment is rejected.
	noID := api.TemplatesCursor("updated_at_desc|2026-05-30T00:00:00Z|")
	_, _, err = parseTemplatesCursor(&noID, templatesSortUpdatedAtDesc)
	require.ErrorIs(t, err, errInvalidTemplatesCursor)
}

func TestNormalizeTemplatesLimit(t *testing.T) {
	t.Parallel()

	one := int32(1)
	huge := int32(1000)
	zero := int32(0)
	mid := int32(25)

	assert.Equal(t, defaultTemplatesLimit, normalizeTemplatesLimit(nil))
	assert.Equal(t, int32(1), normalizeTemplatesLimit(&one))
	assert.Equal(t, maxTemplatesLimit, normalizeTemplatesLimit(&huge))
	assert.Equal(t, int32(1), normalizeTemplatesLimit(&zero))
	assert.Equal(t, mid, normalizeTemplatesLimit(&mid))
}

func TestTemplatesPublicFilter(t *testing.T) {
	t.Parallel()

	yes := true
	no := false

	assert.Equal(t, int16(-1), templatesPublicFilter(nil))
	assert.Equal(t, int16(1), templatesPublicFilter(&yes))
	assert.Equal(t, int16(0), templatesPublicFilter(&no))
}

func TestTemplatesSortValue(t *testing.T) {
	t.Parallel()

	created := time.Date(2026, 5, 30, 12, 34, 56, 123456789, time.UTC)
	updated := time.Date(2026, 5, 29, 1, 2, 3, 0, time.UTC)

	row := templateRowFields{
		TemplateID: "env-abc",
		CreatedAt:  created,
		UpdatedAt:  updated,
	}

	assert.Equal(t, created.Format(time.RFC3339Nano), templatesSortValue(templatesSortCreatedAtAsc, row))
	assert.Equal(t, updated.Format(time.RFC3339Nano), templatesSortValue(templatesSortUpdatedAtDesc, row))
}

func TestCursorTypedParsers(t *testing.T) {
	t.Parallel()

	v := "42"
	n, err := cursorInt64(&v)
	require.NoError(t, err)
	require.NotNil(t, n)
	assert.Equal(t, int64(42), *n)

	n, err = cursorInt64(nil)
	require.NoError(t, err)
	assert.Nil(t, n)

	bad := "not-a-number"
	_, err = cursorInt64(&bad)
	require.ErrorIs(t, err, errInvalidCursor)

	ts := "2026-05-30T12:34:56.123456789Z"
	parsed, err := cursorTime(&ts)
	require.NoError(t, err)
	require.NotNil(t, parsed)

	badTs := "nope"
	_, err = cursorTime(&badTs)
	require.ErrorIs(t, err, errInvalidCursor)

	// Sanity: the sentinel errors are distinct.
	assert.NotErrorIs(t, errInvalidTemplatesCursor, errTemplatesCursorSortMismatch)
}

func TestTemplateRowFieldsToAPI(t *testing.T) {
	t.Parallel()

	desc := "default template"
	row := templateRowFields{
		TemplateID:         "env-abc",
		CpuCount:           2,
		MemoryMb:           1024,
		Aliases:            nil,
		Names:              nil,
		IsDefault:          true,
		DefaultDescription: &desc,
	}

	out := row.toAPI()
	assert.Equal(t, "env-abc", out.TemplateID)
	assert.Equal(t, int64(2), out.CpuCount)
	assert.Equal(t, int64(1024), out.MemoryMB)
	assert.True(t, out.IsDefault)
	require.NotNil(t, out.DefaultDescription)
	assert.Equal(t, desc, *out.DefaultDescription)
	// nil alias/name slices are normalized to empty (non-null) arrays for JSON.
	assert.NotNil(t, out.Aliases)
	assert.NotNil(t, out.Names)
	assert.Nil(t, out.CreatedBy)
}
