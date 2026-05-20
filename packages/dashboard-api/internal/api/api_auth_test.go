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
	noopServer

	receivedUserID uuid.UUID
	hitBootstrap   bool
}

type noopServer struct{}

func (s *authTestServer) PostAdminUsersUserIdBootstrap(c *gin.Context, userId UserId) {
	s.hitBootstrap = true
	s.receivedUserID = userId
	c.Status(http.StatusNoContent)
}

func (noopServer) GetAdminUserProfilesByEmail(c *gin.Context, _ GetAdminUserProfilesByEmailParams) {
	c.Status(http.StatusNotImplemented)
}

func (noopServer) PostAdminUserProfilesResolve(c *gin.Context) {
	c.Status(http.StatusNotImplemented)
}

func (noopServer) GetAdminUserProfilesSearch(c *gin.Context, _ GetAdminUserProfilesSearchParams) {
	c.Status(http.StatusNotImplemented)
}

func (noopServer) GetBuilds(c *gin.Context, _ GetBuildsParams) {
	c.Status(http.StatusNotImplemented)
}

func (noopServer) GetBuildsStatuses(c *gin.Context, _ GetBuildsStatusesParams) {
	c.Status(http.StatusNotImplemented)
}

func (noopServer) GetBuildsBuildId(c *gin.Context, _ BuildId) {
	c.Status(http.StatusNotImplemented)
}

func (noopServer) GetHealth(c *gin.Context) {
	c.Status(http.StatusNotImplemented)
}

func (noopServer) GetSandboxesSandboxIDRecord(c *gin.Context, _ SandboxID) {
	c.Status(http.StatusNotImplemented)
}

func (noopServer) GetTeams(c *gin.Context) {
	c.Status(http.StatusNotImplemented)
}

func (noopServer) PostTeams(c *gin.Context) {
	c.Status(http.StatusNotImplemented)
}

func (noopServer) GetTeamsResolve(c *gin.Context, _ GetTeamsResolveParams) {
	c.Status(http.StatusNotImplemented)
}

func (noopServer) PatchTeamsTeamID(c *gin.Context, _ TeamID) {
	c.Status(http.StatusNotImplemented)
}

func (noopServer) GetTeamsTeamIDMembers(c *gin.Context, _ TeamID) {
	c.Status(http.StatusNotImplemented)
}

func (noopServer) PostTeamsTeamIDMembers(c *gin.Context, _ TeamID) {
	c.Status(http.StatusNotImplemented)
}

func (noopServer) DeleteTeamsTeamIDMembersUserId(c *gin.Context, _ TeamID, _ UserId) {
	c.Status(http.StatusNotImplemented)
}

func (noopServer) GetTemplatesDefaults(c *gin.Context) {
	c.Status(http.StatusNotImplemented)
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
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/admin/users/"+targetUserID.String()+"/bootstrap", nil)
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
