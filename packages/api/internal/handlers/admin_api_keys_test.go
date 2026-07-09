package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	sharedauth "github.com/e2b-dev/infra/packages/auth/pkg/auth"
	authtypes "github.com/e2b-dev/infra/packages/auth/pkg/types"
	authqueries "github.com/e2b-dev/infra/packages/db/pkg/auth/queries"
	"github.com/e2b-dev/infra/packages/db/pkg/testutils"
)

func TestPostAdminTeamsTeamIDApiKeysCreatesTeamKey(t *testing.T) {
	t.Parallel()

	testDB := testutils.SetupDatabase(t)
	teamID := testutils.CreateTestTeam(t, testDB)

	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	ginCtx.Request = httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/admin/teams/"+teamID.String()+"/api-keys", strings.NewReader(`{
		"name": "Admin integration"
	}`))
	ginCtx.Request.Header.Set("Content-Type", "application/json")

	store := &APIStore{
		authDB:      testDB.AuthDB,
		authService: fakeAPIKeyAuthService{team: &authtypes.Team{Team: &authqueries.Team{ID: teamID}}},
	}
	store.PostAdminTeamsTeamIDApiKeys(ginCtx, teamID)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", recorder.Code, recorder.Body.String())
	}

	var body api.CreatedTeamAPIKey
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if body.Id == uuid.Nil {
		t.Fatal("expected API key id")
	}
	if body.Name != "Admin integration" {
		t.Fatalf("expected API key name, got %q", body.Name)
	}
	if body.Key == "" {
		t.Fatal("expected raw API key in response")
	}
	if body.CreatedBy != nil {
		t.Fatalf("expected admin-created key to have nil creator, got %v", *body.CreatedBy)
	}

	keys, err := testDB.AuthDB.Read.GetTeamAPIKeysWithCreator(t.Context(), teamID)
	if err != nil {
		t.Fatalf("failed to list API keys: %v", err)
	}
	if len(keys) != 1 {
		t.Fatalf("expected one API key, got %d", len(keys))
	}
	if keys[0].Name != "Admin integration" {
		t.Fatalf("expected persisted API key name, got %q", keys[0].Name)
	}
	if keys[0].CreatedByID != nil {
		t.Fatalf("expected nil persisted creator, got %v", *keys[0].CreatedByID)
	}
}

func TestPostAdminTeamsTeamIDApiKeysRejectsBlockedTeam(t *testing.T) {
	t.Parallel()

	testDB := testutils.SetupDatabase(t)
	teamID := testutils.CreateTestTeam(t, testDB)
	blockedReason := "payment required"

	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	ginCtx.Request = httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/admin/teams/"+teamID.String()+"/api-keys", strings.NewReader(`{
		"name": "Admin integration"
	}`))
	ginCtx.Request.Header.Set("Content-Type", "application/json")

	store := &APIStore{
		authDB: testDB.AuthDB,
		authService: fakeAPIKeyAuthService{team: &authtypes.Team{Team: &authqueries.Team{
			ID:            teamID,
			IsBlocked:     true,
			BlockedReason: &blockedReason,
		}}},
	}
	store.PostAdminTeamsTeamIDApiKeys(ginCtx, teamID)

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("expected status 403, got %d: %s", recorder.Code, recorder.Body.String())
	}

	keys, err := testDB.AuthDB.Read.GetTeamAPIKeysWithCreator(t.Context(), teamID)
	if err != nil {
		t.Fatalf("failed to list API keys: %v", err)
	}
	if len(keys) != 0 {
		t.Fatalf("expected no API keys for blocked team, got %d", len(keys))
	}
}

func TestPostAdminTeamsTeamIDApiKeysRejectsBannedTeam(t *testing.T) {
	t.Parallel()

	testDB := testutils.SetupDatabase(t)
	teamID := testutils.CreateTestTeam(t, testDB)

	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	ginCtx.Request = httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/admin/teams/"+teamID.String()+"/api-keys", strings.NewReader(`{
		"name": "Admin integration"
	}`))
	ginCtx.Request.Header.Set("Content-Type", "application/json")

	store := &APIStore{
		authDB:      testDB.AuthDB,
		authService: fakeAPIKeyAuthService{err: &sharedauth.TeamForbiddenError{Message: "team is banned"}},
	}
	store.PostAdminTeamsTeamIDApiKeys(ginCtx, teamID)

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("expected status 403, got %d: %s", recorder.Code, recorder.Body.String())
	}
}

func TestPostAdminTeamsTeamIDApiKeysRejectsMissingTeam(t *testing.T) {
	t.Parallel()

	testDB := testutils.SetupDatabase(t)
	teamID := uuid.New()

	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	ginCtx.Request = httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/admin/teams/"+teamID.String()+"/api-keys", strings.NewReader(`{
		"name": "Admin integration"
	}`))
	ginCtx.Request.Header.Set("Content-Type", "application/json")

	store := &APIStore{
		authDB:      testDB.AuthDB,
		authService: fakeAPIKeyAuthService{err: sql.ErrNoRows},
	}
	store.PostAdminTeamsTeamIDApiKeys(ginCtx, teamID)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d: %s", recorder.Code, recorder.Body.String())
	}
}

func TestPostAdminTeamsTeamIDApiKeysRejectsNilTeam(t *testing.T) {
	t.Parallel()

	testDB := testutils.SetupDatabase(t)
	teamID := uuid.New()

	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	ginCtx.Request = httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/admin/teams/"+teamID.String()+"/api-keys", strings.NewReader(`{
		"name": "Admin integration"
	}`))
	ginCtx.Request.Header.Set("Content-Type", "application/json")

	store := &APIStore{
		authDB:      testDB.AuthDB,
		authService: fakeAPIKeyAuthService{},
	}
	store.PostAdminTeamsTeamIDApiKeys(ginCtx, teamID)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d: %s", recorder.Code, recorder.Body.String())
	}
}

func TestDeleteAdminTeamsTeamIDApiKeysDeletesTeamKey(t *testing.T) {
	t.Parallel()

	testDB := testutils.SetupDatabase(t)
	teamID := testutils.CreateTestTeam(t, testDB)

	createRecorder := httptest.NewRecorder()
	createCtx, _ := gin.CreateTestContext(createRecorder)
	createCtx.Request = httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/admin/teams/"+teamID.String()+"/api-keys", strings.NewReader(`{"name":"Admin integration"}`))
	createCtx.Request.Header.Set("Content-Type", "application/json")

	store := &APIStore{
		authDB:      testDB.AuthDB,
		authService: fakeAPIKeyAuthService{team: &authtypes.Team{Team: &authqueries.Team{ID: teamID}}},
	}
	store.PostAdminTeamsTeamIDApiKeys(createCtx, teamID)
	if createRecorder.Code != http.StatusCreated {
		t.Fatalf("expected create status 201, got %d: %s", createRecorder.Code, createRecorder.Body.String())
	}

	var created api.CreatedTeamAPIKey
	if err := json.Unmarshal(createRecorder.Body.Bytes(), &created); err != nil {
		t.Fatalf("failed to decode create response: %v", err)
	}

	deleteRecorder := httptest.NewRecorder()
	deleteCtx, _ := gin.CreateTestContext(deleteRecorder)
	deleteCtx.Request = httptest.NewRequestWithContext(t.Context(), http.MethodDelete, "/admin/teams/"+teamID.String()+"/api-keys/"+created.Id.String(), nil)

	store.DeleteAdminTeamsTeamIDApiKeysApiKeyID(deleteCtx, teamID, created.Id.String())
	if deleteCtx.Writer.Status() != http.StatusNoContent {
		t.Fatalf("expected delete status 204, got %d: %s", deleteCtx.Writer.Status(), deleteRecorder.Body.String())
	}

	keys, err := testDB.AuthDB.Read.GetTeamAPIKeysWithCreator(t.Context(), teamID)
	if err != nil {
		t.Fatalf("failed to list API keys: %v", err)
	}
	if len(keys) != 0 {
		t.Fatalf("expected API key to be deleted, got %d keys", len(keys))
	}
}

func TestDeleteAdminTeamsTeamIDApiKeysRejectsMissingKey(t *testing.T) {
	t.Parallel()

	testDB := testutils.SetupDatabase(t)
	teamID := testutils.CreateTestTeam(t, testDB)

	store := &APIStore{authDB: testDB.AuthDB}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	missingKeyID := uuid.New()
	ctx.Request = httptest.NewRequestWithContext(t.Context(), http.MethodDelete, "/admin/teams/"+teamID.String()+"/api-keys/"+missingKeyID.String(), nil)

	store.DeleteAdminTeamsTeamIDApiKeysApiKeyID(ctx, teamID, missingKeyID.String())

	require.Equal(t, http.StatusNotFound, recorder.Code)
	require.Contains(t, recorder.Header().Get("Content-Type"), "application/json")
	require.JSONEq(t, `{"code":404,"message":"API key not found"}`, recorder.Body.String())
}

type fakeAPIKeyAuthService struct {
	team *authtypes.Team
	err  error
}

func (f fakeAPIKeyAuthService) ValidateAPIKey(context.Context, *gin.Context, string) (*authtypes.Team, *sharedauth.APIError) {
	return nil, nil
}

func (f fakeAPIKeyAuthService) ValidateAccessToken(context.Context, *gin.Context, string) (uuid.UUID, *sharedauth.APIError) {
	return uuid.Nil, nil
}

func (f fakeAPIKeyAuthService) ValidateAuthProviderToken(context.Context, *gin.Context, string) (uuid.UUID, *sharedauth.APIError) {
	return uuid.Nil, nil
}

func (f fakeAPIKeyAuthService) ValidateAuthProviderTeam(context.Context, *gin.Context, string) (*authtypes.Team, *sharedauth.APIError) {
	return nil, nil
}

func (f fakeAPIKeyAuthService) GetTeamByID(context.Context, uuid.UUID) (*authtypes.Team, error) {
	if f.err != nil {
		return nil, f.err
	}

	return f.team, nil
}

func (f fakeAPIKeyAuthService) InvalidateTeamMemberCache(context.Context, uuid.UUID, string) {}

func (f fakeAPIKeyAuthService) InvalidateTeamCache(context.Context, uuid.UUID) error {
	return nil
}

func (f fakeAPIKeyAuthService) Close(context.Context) error {
	return nil
}
