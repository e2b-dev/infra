package utils

import (
	"fmt"
	"testing"

	"github.com/getkin/kin-openapi/openapi3filter"

	sharedauth "github.com/e2b-dev/infra/packages/auth/pkg/auth"
)

func TestProcessCustomErrors_TeamForbiddenAfterNoAuthHeader(t *testing.T) {
	t.Parallel()

	e := &openapi3filter.SecurityRequirementsError{
		Errors: []error{
			sharedauth.ErrNoAuthHeader,
			&sharedauth.TeamForbiddenError{Message: "team is banned"},
		},
	}

	got := processCustomErrors(e)
	want := forbiddenErrPrefix + "team is banned"

	if got.Error() != want {
		t.Errorf("got %q, want %q", got.Error(), want)
	}
}

func TestProcessCustomErrors_TeamBlockedAfterNoAuthHeader(t *testing.T) {
	t.Parallel()

	e := &openapi3filter.SecurityRequirementsError{
		Errors: []error{
			sharedauth.ErrNoAuthHeader,
			&sharedauth.TeamBlockedError{Message: "team is blocked"},
		},
	}

	got := processCustomErrors(e)
	want := blockedErrPrefix + "team is blocked"

	if got.Error() != want {
		t.Errorf("got %q, want %q", got.Error(), want)
	}
}

func TestProcessCustomErrors_TeamForbiddenOnly(t *testing.T) {
	t.Parallel()

	e := &openapi3filter.SecurityRequirementsError{
		Errors: []error{
			&sharedauth.TeamForbiddenError{Message: "team is banned"},
		},
	}

	got := processCustomErrors(e)
	want := forbiddenErrPrefix + "team is banned"

	if got.Error() != want {
		t.Errorf("got %q, want %q", got.Error(), want)
	}
}

func TestProcessCustomErrors_TeamBlockedOnly(t *testing.T) {
	t.Parallel()

	e := &openapi3filter.SecurityRequirementsError{
		Errors: []error{
			&sharedauth.TeamBlockedError{Message: "team is blocked"},
		},
	}

	got := processCustomErrors(e)
	want := blockedErrPrefix + "team is blocked"

	if got.Error() != want {
		t.Errorf("got %q, want %q", got.Error(), want)
	}
}

func TestProcessCustomErrors_GenericErrorAfterNoAuthHeader(t *testing.T) {
	t.Parallel()

	genericErr := &openapi3filter.SecurityRequirementsError{
		Errors: []error{
			sharedauth.ErrNoAuthHeader,
			&openapi3filter.SecurityRequirementsError{SecurityRequirements: nil},
		},
	}

	got := processCustomErrors(genericErr)

	if got == nil {
		t.Fatal("expected non-nil error")
	}
}

func TestProcessCustomErrors_AllNoAuthHeader(t *testing.T) {
	t.Parallel()

	e := &openapi3filter.SecurityRequirementsError{
		Errors: []error{
			sharedauth.ErrNoAuthHeader,
			sharedauth.ErrNoAuthHeader,
		},
	}

	got := processCustomErrors(e)
	want := securityErrPrefix + sharedauth.ErrNoAuthHeader.Error()

	if got.Error() != want {
		t.Errorf("got %q, want %q", got.Error(), want)
	}
}

func TestProcessCustomErrors_WrappedTeamForbidden(t *testing.T) {
	t.Parallel()

	inner := &sharedauth.TeamForbiddenError{Message: "team is banned"}
	wrapped := fmt.Errorf("failed getting team: %w", inner)

	e := &openapi3filter.SecurityRequirementsError{
		Errors: []error{wrapped},
	}

	got := processCustomErrors(e)
	want := forbiddenErrPrefix + "team is banned"

	if got.Error() != want {
		t.Errorf("got %q, want %q", got.Error(), want)
	}
}

func TestProcessCustomErrors_WrappedTeamBlocked(t *testing.T) {
	t.Parallel()

	inner := &sharedauth.TeamBlockedError{Message: "team is blocked: payment overdue"}
	wrapped := fmt.Errorf("failed getting team: %w", inner)

	e := &openapi3filter.SecurityRequirementsError{
		Errors: []error{wrapped},
	}

	got := processCustomErrors(e)
	want := blockedErrPrefix + "team is blocked: payment overdue"

	if got.Error() != want {
		t.Errorf("got %q, want %q", got.Error(), want)
	}
}
