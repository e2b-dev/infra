package utils

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/api/internal/api"
)

func sandboxWithTemplate(sandboxID, templateID string) PaginatedSandbox {
	return PaginatedSandbox{
		ListedSandbox: api.ListedSandbox{
			SandboxID:  sandboxID,
			TemplateID: templateID,
		},
	}
}

func TestFilterSandboxesOnTemplate(t *testing.T) {
	t.Parallel()

	sandboxes := []PaginatedSandbox{
		sandboxWithTemplate("sbx-1", "tmpl-a"),
		sandboxWithTemplate("sbx-2", "tmpl-b"),
		sandboxWithTemplate("sbx-3", "tmpl-a"),
	}

	t.Run("nil filter returns all", func(t *testing.T) {
		t.Parallel()

		input := append([]PaginatedSandbox(nil), sandboxes...)
		actual := FilterSandboxesOnTemplate(input, nil)
		assert.Len(t, actual, 3)
	})

	t.Run("empty string filter returns all", func(t *testing.T) {
		t.Parallel()

		empty := ""
		input := append([]PaginatedSandbox(nil), sandboxes...)
		actual := FilterSandboxesOnTemplate(input, &empty)
		assert.Len(t, actual, 3)
	})

	t.Run("matching template returns subset", func(t *testing.T) {
		t.Parallel()

		templateID := "tmpl-a"
		input := append([]PaginatedSandbox(nil), sandboxes...)
		actual := FilterSandboxesOnTemplate(input, &templateID)
		require.Len(t, actual, 2)
		assert.Equal(t, "sbx-1", actual[0].SandboxID)
		assert.Equal(t, "sbx-3", actual[1].SandboxID)
	})

	t.Run("no match returns empty", func(t *testing.T) {
		t.Parallel()

		templateID := "tmpl-missing"
		input := append([]PaginatedSandbox(nil), sandboxes...)
		actual := FilterSandboxesOnTemplate(input, &templateID)
		assert.Empty(t, actual)
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
