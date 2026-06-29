package utils

import (
	"testing"

	"github.com/getkin/kin-openapi/openapi3filter"

	sharedauth "github.com/e2b-dev/infra/packages/auth/pkg/auth"
)

func TestProcessCustomErrors_TeamForbiddenAfterNoAuthHeader(t *testing.T) {
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
