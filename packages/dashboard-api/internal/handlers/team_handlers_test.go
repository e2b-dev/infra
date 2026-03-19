package handlers

import (
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestMapAddTeamMemberError(t *testing.T) {
	status, message, ok := mapAddTeamMemberError(&pgconn.PgError{Code: "23505"})
	if !ok {
		t.Fatalf("expected duplicate error to be handled")
	}
	if status != 400 {
		t.Fatalf("expected status 400, got %d", status)
	}
	if message != "User is already a member of this team" {
		t.Fatalf("unexpected message: %s", message)
	}
}

func TestMapRemoveTeamMemberRows(t *testing.T) {
	status, message, ok := mapRemoveTeamMemberRows(0)
	if !ok {
		t.Fatalf("expected zero rows to be handled")
	}
	if status != 400 {
		t.Fatalf("expected status 400, got %d", status)
	}
	if message != "User is not a member of this team" {
		t.Fatalf("unexpected message: %s", message)
	}
}

func TestParseUpdateTeamBody_ProfilePictureNullClearsValue(t *testing.T) {
	body, err := parseUpdateTeamBody(strings.NewReader(`{"profilePictureUrl":null}`))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !body.ProfilePictureUrlSet {
		t.Fatalf("expected profilePictureUrl to be marked as set")
	}
	if body.ProfilePictureUrl != nil {
		t.Fatalf("expected nil profilePictureUrl for explicit null")
	}
}

func TestParseUpdateTeamBody_ProfilePictureOmittedIsNoop(t *testing.T) {
	body, err := parseUpdateTeamBody(strings.NewReader(`{"name":"team-a"}`))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if body.ProfilePictureUrlSet {
		t.Fatalf("expected profilePictureUrl to be unset when omitted")
	}
}

func TestParseUpdateTeamBody_NameNullRejected(t *testing.T) {
	_, err := parseUpdateTeamBody(strings.NewReader(`{"name":null}`))
	if err == nil {
		t.Fatalf("expected error for null name")
	}
}
