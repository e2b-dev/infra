package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/auth/pkg/auth"
	authtypes "github.com/e2b-dev/infra/packages/auth/pkg/types"
	authqueries "github.com/e2b-dev/infra/packages/db/pkg/auth/queries"
)

func TestMapAddTeamMemberRows(t *testing.T) {
	t.Parallel()

	status, message, ok := mapAddTeamMemberRows(0)
	if !ok {
		t.Fatalf("expected zero rows to be handled")
	}
	if status != 400 {
		t.Fatalf("expected status 400, got %d", status)
	}
	if message != "User is already a member of this team" {
		t.Fatalf("unexpected message: %s", message)
	}
}

func TestMapRemoveTeamMemberRows(t *testing.T) {
	t.Parallel()

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
	t.Parallel()

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
	t.Parallel()

	body, err := parseUpdateTeamBody(strings.NewReader(`{"name":"team-a"}`))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if body.ProfilePictureUrlSet {
		t.Fatalf("expected profilePictureUrl to be unset when omitted")
	}
}

func TestParseUpdateTeamBody_NameNullRejected(t *testing.T) {
	t.Parallel()

	_, err := parseUpdateTeamBody(strings.NewReader(`{"name":null}`))
	if err == nil {
		t.Fatalf("expected error for null name")
	}
}

func TestRequireAuthedTeamMatchesPath_Success(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)

	teamID := uuid.New()
	auth.SetTeamInfo(ctx, &authtypes.Team{
		Team: &authqueries.Team{ID: teamID},
	})

	store := &APIStore{}
	_, ok := store.requireAuthedTeamMatchesPath(ctx, teamID)
	if !ok {
		t.Fatalf("expected team parity check to pass")
	}
}

func TestRequireAuthedTeamMatchesPath_Mismatch(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)

	auth.SetTeamInfo(ctx, &authtypes.Team{
		Team: &authqueries.Team{ID: uuid.New()},
	})

	store := &APIStore{}
	_, ok := store.requireAuthedTeamMatchesPath(ctx, uuid.New())
	if ok {
		t.Fatalf("expected team parity check to fail")
	}
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("expected status 403, got %d", recorder.Code)
	}
}
