package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/auth/pkg/types"
	authqueries "github.com/e2b-dev/infra/packages/db/pkg/auth/queries"
)

func TestAdminValidationFunction(t *testing.T) {
	t.Parallel()

	validate := adminValidationFunction("super-secret-token")

	t.Run("accepts matching token", func(t *testing.T) {
		t.Parallel()

		_, err := validate(t.Context(), nil, "super-secret-token")
		require.Nil(t, err)
	})

	t.Run("rejects non-matching token", func(t *testing.T) {
		t.Parallel()

		_, err := validate(t.Context(), nil, "super-secret-tokem")
		require.NotNil(t, err)
		require.Equal(t, 401, err.Code)
	})
}

func TestAdminTeamAuthenticatorSetsTeamContext(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	teamID := uuid.New()
	team := types.NewTeam(&authqueries.Team{ID: teamID}, &authqueries.TeamLimit{})

	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/", nil)
	req.Header.Set(HeaderTeamID, teamID.String())

	ginCtx, _ := gin.CreateTestContext(httptest.NewRecorder())
	authenticator := NewAdminTeamAuthenticator(func(_ context.Context, _ *gin.Context, gotTeamID string) (*types.Team, *APIError) {
		if gotTeamID != teamID.String() {
			return nil, &APIError{
				Err:       ErrInvalidAuthHeader,
				ClientMsg: "Invalid team ID",
				Code:      http.StatusBadRequest,
			}
		}

		return team, nil
	})
	if got, want := authenticator.SecuritySchemeName(), "AdminTeamAuth"; got != want {
		t.Fatalf("NewAdminTeamAuthenticator().SecuritySchemeName() = %q, want %q", got, want)
	}

	err := authenticator.Authenticate(ctx, ginCtx, &openapi3filter.AuthenticationInput{
		RequestValidationInput: &openapi3filter.RequestValidationInput{Request: req},
	})
	if err != nil {
		t.Fatalf("AdminTeamAuth.Authenticate(valid team ID) error: %v", err)
	}

	got, ok := GetTeamInfo(ginCtx)
	if !ok {
		t.Fatalf("GetTeamInfo(ginCtx) ok = false, want true")
	}

	if got.Team.ID != teamID {
		t.Errorf("GetTeamInfo(ginCtx).Team.ID = %s, want %s", got.Team.ID, teamID)
	}
}
