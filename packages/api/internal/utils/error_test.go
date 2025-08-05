package utils

import (
	"errors"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/api/internal/auth"
	"github.com/e2b-dev/infra/packages/api/internal/db"
)

func TestMultiErrorHandler(t *testing.T) {
	t.Run("empty slice", func(t *testing.T) {
		actual := MultiErrorHandler(openapi3.MultiError{})
		assert.Nil(t, actual)
	})

	testcases := map[string]struct {
		input           openapi3.MultiError
		expectedMessage string
	}{
		"has request error": {
			input: openapi3.MultiError{
				&openapi3filter.RequestError{
					Reason: "some reason",
				},
			},
			expectedMessage: "error in openapi3filter.RequestError: some reason",
		},
		"has team forbidden": {
			input: openapi3.MultiError{
				&openapi3filter.SecurityRequirementsError{
					Errors: []error{
						&db.TeamForbiddenError{},
					},
				},
			},
			expectedMessage: "team forbidden: ",
		},
		"has team blocked": {
			input: openapi3.MultiError{
				&openapi3filter.SecurityRequirementsError{
					Errors: []error{
						&db.TeamBlockedError{},
					},
				},
			},
			expectedMessage: "team blocked: ",
		},
		"no auth header is ignored": {
			input: openapi3.MultiError{
				&openapi3filter.SecurityRequirementsError{
					Errors: []error{
						auth.ErrNoAuthHeader,
						&db.TeamBlockedError{},
					},
				},
			},
			expectedMessage: "team blocked: ",
		},
		"other errors return default prefix": {
			input: openapi3.MultiError{
				&openapi3filter.SecurityRequirementsError{
					Errors: []error{
						auth.ErrNoAuthHeader,
						errors.New("some error"),
					},
				},
			},
			expectedMessage: "error in openapi3filter.SecurityRequirementsError: security requirements failed: some error",
		},
	}

	for name, tc := range testcases {
		t.Run(name, func(t *testing.T) {
			actual := MultiErrorHandler(tc.input)
			require.Error(t, actual)
			assert.Equal(t, tc.expectedMessage, actual.Error())
		})
	}
}
