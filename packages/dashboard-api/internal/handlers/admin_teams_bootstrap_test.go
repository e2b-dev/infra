package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	internalteamprovision "github.com/e2b-dev/infra/packages/dashboard-api/internal/teamprovision"
	"github.com/e2b-dev/infra/packages/db/pkg/testutils"
	"github.com/e2b-dev/infra/packages/shared/pkg/teamprovision"
)

func TestPostAdminTeamsBootstrapCreatesTeam(t *testing.T) {
	t.Parallel()

	testDB := testutils.SetupDatabase(t)
	ctx := t.Context()
	sink := &fakeTeamProvisionSink{}

	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	ginCtx.Request = httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/teams/bootstrap", strings.NewReader(`{
		"name": "  Bootstrap team  ",
		"email": "bootstrap-team@example.com"
	}`))
	ginCtx.Request.Header.Set("Content-Type", "application/json")

	store := &APIStore{
		authDB:            testDB.AuthDB,
		teamProvisionSink: sink,
	}
	store.PostAdminTeamsBootstrap(ginCtx)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", recorder.Code, recorder.Body.String())
	}

	var body struct {
		ID   uuid.UUID `json:"id"`
		Slug string    `json:"slug"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if body.ID == uuid.Nil {
		t.Fatal("expected created team id")
	}
	if body.Slug == "" {
		t.Fatal("expected created team slug")
	}

	var name, email string
	if err := testDB.SqlcClient.TestsRawSQLQuery(ctx, `SELECT name, email FROM public.teams WHERE id = $1`, func(rows pgx.Rows) error {
		if !rows.Next() {
			return pgx.ErrNoRows
		}

		return rows.Scan(&name, &email)
	}, body.ID); err != nil {
		t.Fatalf("failed to query created team: %v", err)
	}
	if name != "Bootstrap team" {
		t.Fatalf("expected trimmed name, got %q", name)
	}
	if email != "bootstrap-team@example.com" {
		t.Fatalf("expected email, got %q", email)
	}

	if len(sink.requests) != 1 {
		t.Fatalf("expected one provisioning request, got %d", len(sink.requests))
	}
	req := sink.requests[0]
	if req.TeamID != body.ID || req.TeamName != name || req.TeamEmail != email {
		t.Fatalf("unexpected provisioning request: %+v", req)
	}
	if req.CreatorUserID != uuid.Nil {
		t.Fatalf("expected bootstrap creator user id, got %s", req.CreatorUserID)
	}
	if req.Reason != teamprovision.ReasonAdditionalTeam {
		t.Fatalf("expected additional team reason, got %q", req.Reason)
	}
}

func TestPostAdminTeamsBootstrapRollsBackOnProvisioningFailure(t *testing.T) {
	t.Parallel()

	testDB := testutils.SetupDatabase(t)
	ctx := t.Context()
	sink := &fakeTeamProvisionSink{
		err: &internalteamprovision.ProvisionError{
			StatusCode: http.StatusBadGateway,
			Message:    "billing unavailable",
		},
	}

	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	ginCtx.Request = httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/teams/bootstrap", strings.NewReader(`{
		"name": "Bootstrap team",
		"email": "bootstrap-team@example.com"
	}`))
	ginCtx.Request.Header.Set("Content-Type", "application/json")

	store := &APIStore{
		authDB:            testDB.AuthDB,
		teamProvisionSink: sink,
	}
	store.PostAdminTeamsBootstrap(ginCtx)

	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("expected status 502, got %d: %s", recorder.Code, recorder.Body.String())
	}
	if len(sink.requests) != 1 {
		t.Fatalf("expected one provisioning request, got %d", len(sink.requests))
	}

	var count int
	if err := testDB.SqlcClient.TestsRawSQLQuery(ctx, `SELECT count(*) FROM public.teams WHERE email = $1`, func(rows pgx.Rows) error {
		if !rows.Next() {
			return pgx.ErrNoRows
		}

		return rows.Scan(&count)
	}, "bootstrap-team@example.com"); err != nil {
		t.Fatalf("failed to count teams: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected rolled back team, found %d", count)
	}
}

func TestPostAdminTeamsBootstrapRejectsMissingFields(t *testing.T) {
	t.Parallel()

	testDB := testutils.SetupDatabase(t)
	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	ginCtx.Request = httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/admin/teams/bootstrap", strings.NewReader(`{
		"name": "Bootstrap team",
		"email": ""
	}`))
	ginCtx.Request.Header.Set("Content-Type", "application/json")

	store := &APIStore{
		authDB:            testDB.AuthDB,
		teamProvisionSink: &fakeTeamProvisionSink{err: errors.New("should not provision")},
	}
	store.PostAdminTeamsBootstrap(ginCtx)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d: %s", recorder.Code, recorder.Body.String())
	}
}
