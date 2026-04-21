package api

import (
	"context"
	"errors"
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
	s.receivedUserID = userId
	c.Status(http.StatusNoContent)
}

func (s *authTestServer) GetBuilds(_ *gin.Context, _ GetBuildsParams) {
	panic("unexpected call to GetBuilds")
}

func (s *authTestServer) GetBuildsStatuses(_ *gin.Context, _ GetBuildsStatusesParams) {
	panic("unexpected call to GetBuildsStatuses")
}

func (s *authTestServer) GetBuildsBuildId(_ *gin.Context, _ BuildId) {
	panic("unexpected call to GetBuildsBuildId")
}

func (s *authTestServer) GetHealth(_ *gin.Context) {
	panic("unexpected call to GetHealth")
}

func (s *authTestServer) GetSandboxesSandboxIDRecord(_ *gin.Context, _ SandboxID) {
	panic("unexpected call to GetSandboxesSandboxIDRecord")
}

func (s *authTestServer) GetTeams(_ *gin.Context) {
	panic("unexpected call to GetTeams")
}

func (s *authTestServer) PostTeams(_ *gin.Context) {
	panic("unexpected call to PostTeams")
}

func (s *authTestServer) GetTeamsResolve(_ *gin.Context, _ GetTeamsResolveParams) {
	panic("unexpected call to GetTeamsResolve")
}

func (s *authTestServer) PatchTeamsTeamID(_ *gin.Context, _ TeamID) {
	panic("unexpected call to PatchTeamsTeamID")
}

func (s *authTestServer) GetTeamsTeamIDMembers(_ *gin.Context, _ TeamID) {
	panic("unexpected call to GetTeamsTeamIDMembers")
}

func (s *authTestServer) PostTeamsTeamIDMembers(_ *gin.Context, _ TeamID) {
	panic("unexpected call to PostTeamsTeamIDMembers")
}

func (s *authTestServer) DeleteTeamsTeamIDMembersUserId(_ *gin.Context, _ TeamID, _ UserId) {
	panic("unexpected call to DeleteTeamsTeamIDMembersUserId")
}

func (s *authTestServer) GetTemplatesDefaults(_ *gin.Context) {
	panic("unexpected call to GetTemplatesDefaults")
}

func TestAdminBootstrapRoute_AcceptsAdminTokenOnly(t *testing.T) {
	t.Parallel()

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

				return uuid.Nil, &sharedauth.APIError{Code: http.StatusUnauthorized, ClientMsg: "unexpected", Err: errors.New("unexpected supabase auth call")}
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
