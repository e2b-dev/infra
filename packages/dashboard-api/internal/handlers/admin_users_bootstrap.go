package handlers

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/dashboard-api/internal/api"
	"github.com/e2b-dev/infra/packages/shared/pkg/ginutils"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (s *APIStore) PostAdminUsersUserIdBootstrap(c *gin.Context, userId api.UserId) {
	ctx := c.Request.Context()
	telemetry.ReportEvent(ctx, "bootstrap user")

	team, err := s.bootstrapSupabaseUser(ctx, userId)
	if err != nil {
		s.handleProvisioningError(ctx, c, "bootstrap user", err)

		return
	}

	c.JSON(http.StatusOK, api.TeamResolveResponse{
		Id:   team.ID,
		Slug: team.Slug,
	})
}

func (s *APIStore) PostAdminUsersBootstrap(c *gin.Context) {
	ctx := c.Request.Context()
	telemetry.ReportEvent(ctx, "bootstrap auth provider user")

	body, err := ginutils.ParseBody[api.AdminAuthProviderUserBootstrapRequest](ctx, c)
	if err != nil {
		telemetry.ReportErrorByCode(ctx, http.StatusBadRequest, "bootstrap auth provider user failed", fmt.Errorf("parse bootstrap auth provider user request: %w", err))
		s.sendAPIStoreError(c, http.StatusBadRequest, "Invalid request body")

		return
	}

	oidcUserID := strings.TrimSpace(body.OidcUserId)
	oidcUserEmail := strings.TrimSpace(string(body.OidcUserEmail))
	if oidcUserID == "" || oidcUserEmail == "" {
		telemetry.ReportErrorByCode(ctx, http.StatusBadRequest, "bootstrap auth provider user failed", fmt.Errorf("oidc_user_id and oidc_user_email must be non-empty"))
		s.sendAPIStoreError(c, http.StatusBadRequest, "oidc_user_id and oidc_user_email must be non-empty")

		return
	}

	team, err := s.bootstrapOIDCUser(ctx, oidcUserBootstrapInput{
		OIDCUserID:    oidcUserID,
		OIDCUserEmail: oidcUserEmail,
		OIDCUserName:  body.OidcUserName,
	})
	if err != nil {
		s.handleProvisioningError(ctx, c, "bootstrap auth provider user", err)

		return
	}

	c.JSON(http.StatusOK, api.TeamResolveResponse{
		Id:   team.ID,
		Slug: team.Slug,
	})
}
