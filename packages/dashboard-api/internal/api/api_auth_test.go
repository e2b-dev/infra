package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	ginmiddleware "github.com/oapi-codegen/gin-middleware"

	sharedauth "github.com/e2b-dev/infra/packages/auth/pkg/auth"
)

type authTestServer struct {
	receivedUserID uuid.UUID
	hitBootstrap   bool
}

func (s *authTestServer) PostAdminUsersUserIdBootstrap(c *gin.Context, userId UserId) {
	s.hitBootstrap = true
	s.receivedUserID = uuid.UUID(userId)
	c.Status(http.StatusNoContent)
}

func (s *authTestServer) GetBuilds(c *gin.Context, params GetBuildsParams) {
	panic("unexpected call to GetBuilds")
}

func (s *authTestServer) GetBuildsStatuses(c *gin.Context, params GetBuildsStatusesParams) {
	panic("unexpected call to GetBuildsStatuses")
}

func (s *authTestServer) GetBuildsBuildId(c *gin.Context, buildId BuildId) {
	panic("unexpected call to GetBuildsBuildId")
}

func (s *authTestServer) GetHealth(c *gin.Context) {
	panic("unexpected call to GetHealth")
}

func (s *authTestServer) GetSandboxesSandboxIDRecord(c *gin.Context, sandboxID SandboxID) {
	panic("unexpected call to GetSandboxesSandboxIDRecord")
}

func (s *authTestServer) GetTeams(c *gin.Context) {
	panic("unexpected call to GetTeams")
}

func (s *authTestServer) PostTeams(c *gin.Context) {
	panic("unexpected call to PostTeams")
}

func (s *authTestServer) GetTeamsResolve(c *gin.Context, params GetTeamsResolveParams) {
	panic("unexpected call to GetTeamsResolve")
}

func (s *authTestServer) PatchTeamsTeamID(c *gin.Context, teamID TeamID) {
	panic("unexpected call to PatchTeamsTeamID")
}

func (s *authTestServer) GetTeamsTeamIDMembers(c *gin.Context, teamID TeamID) {
	panic("unexpected call to GetTeamsTeamIDMembers")
}

func (s *authTestServer) PostTeamsTeamIDMembers(c *gin.Context, teamID TeamID) {
	panic("unexpected call to PostTeamsTeamIDMembers")
}

func (s *authTestServer) DeleteTeamsTeamIDMembersUserId(c *gin.Context, teamID TeamID, userId UserId) {
	panic("unexpected call to DeleteTeamsTeamIDMembersUserId")
}

func (s *authTestServer) GetTemplatesDefaults(c *gin.Context) {
	panic("unexpected call to GetTemplatesDefaults")
}

func TestAdminBootstrapRoute_AcceptsAdminTokenOnly(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)

	server := &authTestServer{}
	swagger, err := GetSwagger()
	if err != nil {
		t.Fatalf("failed to load swagger: %v", err)
	}
	swagger.Servers = nil

	supabaseCalled := false
	authenticationFunc := sharedauth.CreateAuthenticationFunc(
		[]sharedauth.Authenticator{
			sharedauth.NewAdminTokenAuthenticator("super-secret-token"),
			sharedauth.NewSupabaseTokenAuthenticator(func(_ context.Context, _ *gin.Context, _ string) (uuid.UUID, *sharedauth.APIError) {
				supabaseCalled = true
				return uuid.Nil, &sharedauth.APIError{Code: http.StatusUnauthorized, ClientMsg: "unexpected", Err: fmt.Errorf("unexpected supabase auth call")}
			}),
		},
		nil,
	)

	r := gin.New()
	r.Use(ginmiddleware.OapiRequestValidatorWithOptions(swagger, &ginmiddleware.Options{
		ErrorHandler: func(c *gin.Context, message string, statusCode int) {
			c.AbortWithStatusJSON(statusCode, gin.H{"code": statusCode, "message": message})
		},
		MultiErrorHandler: func(me openapi3.MultiError) error {
			msgs := make([]string, 0, len(me))
			for _, e := range me {
				msgs = append(msgs, e.Error())
			}

			return fmt.Errorf("%s", strings.Join(msgs, "; "))
		},
		Options: openapi3filter.Options{AuthenticationFunc: authenticationFunc},
	}))
	RegisterHandlers(r, server)

	targetUserID := uuid.New()
	req := httptest.NewRequest(http.MethodPost, "/admin/users/"+targetUserID.String()+"/bootstrap", nil)
	req.Header.Set(sharedauth.HeaderAdminToken, "super-secret-token")
	recorder := httptest.NewRecorder()

	r.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("expected status 204, got %d with body %s", recorder.Code, recorder.Body.String())
	}
	if !server.hitBootstrap {
		t.Fatal("expected bootstrap handler to be called")
	}
	if server.receivedUserID != targetUserID {
		t.Fatalf("expected user id %s, got %s", targetUserID, server.receivedUserID)
	}
	if supabaseCalled {
		t.Fatal("expected route to authenticate without calling Supabase auth")
	}
}
