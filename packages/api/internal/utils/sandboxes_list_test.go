package utils

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
