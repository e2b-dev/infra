package handlers

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/dashboard-api/internal/api"
	"github.com/e2b-dev/infra/packages/shared/pkg/ginutils"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

// PostAdminUsersUserIdBootstrap is the legacy bootstrap path; effectively
// deprecated and only used by Supabase-backed deployments. New OIDC-based
// dashboard setups should use PostAdminUsersBootstrap.
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

// PostAdminUsersBootstrap is the new bootstrap entry point for dashboards
// using a generic OIDC provider.
func (s *APIStore) PostAdminUsersBootstrap(c *gin.Context) {
	ctx := c.Request.Context()
	telemetry.ReportEvent(ctx, "bootstrap auth provider user")

	body, err := ginutils.ParseBody[api.AdminAuthProviderUserBootstrapRequest](ctx, c)
	if err != nil {
		telemetry.ReportErrorByCode(ctx, http.StatusBadRequest, "bootstrap auth provider user failed", fmt.Errorf("parse bootstrap auth provider user request: %w", err))
		s.sendAPIStoreError(c, http.StatusBadRequest, "Invalid request body")

		return
	}

	oidcIssuer := strings.TrimSpace(body.OidcIssuer)
	oidcUserID := strings.TrimSpace(body.OidcUserId)
	oidcUserEmail := strings.TrimSpace(string(body.OidcUserEmail))
	if oidcIssuer == "" || oidcUserID == "" || oidcUserEmail == "" {
		telemetry.ReportErrorByCode(ctx, http.StatusBadRequest, "bootstrap auth provider user failed", errors.New("oidc_issuer, oidc_user_id and oidc_user_email must be non-empty"))
		s.sendAPIStoreError(c, http.StatusBadRequest, "oidc_issuer, oidc_user_id and oidc_user_email must be non-empty")

		return
	}

	team, err := s.bootstrapOIDCUser(ctx, oidcUserBootstrapInput{
		OIDCIssuer:      oidcIssuer,
		OIDCUserID:      oidcUserID,
		OIDCUserEmail:   oidcUserEmail,
		OIDCUserName:    body.OidcUserName,
		SignupIP:        strings.TrimSpace(valueOrEmpty(body.SignupIp)),
		SignupUserAgent: strings.TrimSpace(valueOrEmpty(body.SignupUserAgent)),
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

func valueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}

	return *value
}
