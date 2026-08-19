package utils

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	sharedauth "github.com/e2b-dev/infra/packages/auth/pkg/auth"
)

func TestMultiErrorHandlerSecuritySelection(t *testing.T) {
	t.Parallel()

	missingHeader := fmt.Errorf("Invalid API key. %w", sharedauth.ErrNoAuthHeader)
	forbidden := &sharedauth.TeamForbiddenError{Message: "team is banned"}

	testCases := map[string]struct {
		errs []error
		want string
	}{
		"forbidden after missing header": {
			errs: []error{missingHeader, forbidden},
			want: sharedauth.ForbiddenErrPrefix + "team is banned",
		},
		"forbidden behind an attempted invalid credential": {
			errs: []error{errors.New("invalid key format"), forbidden},
			want: sharedauth.ForbiddenErrPrefix + "team is banned",
		},
		"all missing headers falls back to the first": {
			errs: []error{missingHeader, missingHeader},
			want: sharedauth.SecurityErrPrefix + missingHeader.Error(),
		},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := MultiErrorHandler(openapi3.MultiError{
				&openapi3filter.SecurityRequirementsError{Errors: tc.errs},
			})
			assert.Equal(t, tc.want, got.Error())
		})
	}
}

// runErrorHandler feeds a MultiErrorHandler result through ErrorHandler the
// way the request-validator middleware does.
func runErrorHandler(t *testing.T, message string, statusCode int) (int, string) {
	t.Helper()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/sandboxes", nil)

	ErrorHandler(c, message, statusCode)

	var body struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))

	return w.Code, body.Message
}

func TestErrorHandlerSecurityStatusMapping(t *testing.T) {
	t.Parallel()

	forbidden := &sharedauth.TeamForbiddenError{Message: "team is banned"}

	testCases := map[string]struct {
		errs        []error
		wantCode    int
		wantMessage string
	}{
		"forbidden team maps to 403": {
			errs:        []error{fmt.Errorf("Invalid API key. %w", sharedauth.ErrNoAuthHeader), forbidden},
			wantCode:    http.StatusForbidden,
			wantMessage: "team is banned",
		},
		"forbidden behind an attempted invalid credential maps to 403": {
			errs:        []error{errors.New("invalid key format"), forbidden},
			wantCode:    http.StatusForbidden,
			wantMessage: "team is banned",
		},
		"attempted scheme keeps the middleware status": {
			errs:        []error{errors.New("invalid key format")},
			wantCode:    http.StatusUnauthorized,
			wantMessage: "invalid key format",
		},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			selected := MultiErrorHandler(openapi3.MultiError{
				&openapi3filter.SecurityRequirementsError{Errors: tc.errs},
			})

			code, message := runErrorHandler(t, selected.Error(), http.StatusUnauthorized)
			assert.Equal(t, tc.wantCode, code)
			assert.Equal(t, tc.wantMessage, message)
		})
	}
}
