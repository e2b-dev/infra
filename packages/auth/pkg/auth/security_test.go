package auth_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/stretchr/testify/assert"

	"github.com/e2b-dev/infra/packages/auth/pkg/auth"
)

func TestProcessSecurityErrors(t *testing.T) {
	t.Parallel()

	missingHeader := fmt.Errorf("Invalid API key. %w", auth.ErrNoAuthHeader)
	attempted := errors.New("Invalid API key format")
	forbidden := &auth.TeamForbiddenError{Message: "team is banned"}
	blocked := &auth.TeamBlockedError{Message: "team is blocked"}

	testCases := map[string]struct {
		errs []error
		want string
	}{
		"forbidden after missing header": {
			errs: []error{missingHeader, forbidden},
			want: auth.ForbiddenErrPrefix + "team is banned",
		},
		"blocked after missing header": {
			errs: []error{missingHeader, blocked},
			want: auth.BlockedErrPrefix + "team is blocked",
		},
		"forbidden only": {
			errs: []error{forbidden},
			want: auth.ForbiddenErrPrefix + "team is banned",
		},
		"blocked only": {
			errs: []error{blocked},
			want: auth.BlockedErrPrefix + "team is blocked",
		},
		"forbidden behind an attempted invalid credential": {
			errs: []error{attempted, forbidden},
			want: auth.ForbiddenErrPrefix + "team is banned",
		},
		"blocked behind an attempted invalid credential": {
			errs: []error{attempted, blocked},
			want: auth.BlockedErrPrefix + "team is blocked",
		},
		"wrapped forbidden": {
			errs: []error{fmt.Errorf("failed getting team: %w", forbidden)},
			want: auth.ForbiddenErrPrefix + "team is banned",
		},
		"wrapped blocked": {
			errs: []error{fmt.Errorf("failed getting team: %w", blocked)},
			want: auth.BlockedErrPrefix + "team is blocked",
		},
		"attempted scheme wins over missing headers": {
			errs: []error{missingHeader, attempted, missingHeader},
			want: auth.SecurityErrPrefix + "Invalid API key format",
		},
		"all missing headers falls back to the first": {
			errs: []error{missingHeader, missingHeader},
			want: auth.SecurityErrPrefix + missingHeader.Error(),
		},
		"no errors": {
			errs: nil,
			want: auth.SecurityErrPrefix + "authentication failed",
		},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := auth.ProcessSecurityErrors(&openapi3filter.SecurityRequirementsError{Errors: tc.errs})
			assert.Equal(t, tc.want, got.Error())
		})
	}
}
