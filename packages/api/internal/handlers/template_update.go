package handlers

import (
	"context"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/db/pkg/dberrors"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/id"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

// PatchTemplatesTemplateID serves to update a template (v1 - deprecated, for older CLIs, creates backward-compatible aliases)
func (a *APIStore) PatchTemplatesTemplateID(c *gin.Context, aliasOrTemplateID api.TemplateID) {
	if a.updateTemplate(c, aliasOrTemplateID, true) == nil {
		return
	}

	c.JSON(http.StatusOK, nil)
}

// PatchV2TemplatesTemplateID serves to update a template (v2 - for new CLIs)
func (a *APIStore) PatchV2TemplatesTemplateID(c *gin.Context, aliasOrTemplateID api.TemplateID) {
	result := a.updateTemplate(c, aliasOrTemplateID, false)
	if result == nil {
		return
	}

	c.JSON(http.StatusOK, api.TemplateUpdateResponse{
		Names: result.Names,
	})
}

type templateUpdateResult struct {
	Names []string
}

// updateTemplate contains the shared logic for updating a template.
// Returns nil if an error occurred (error response already sent), or the result on success.
func (a *APIStore) updateTemplate(c *gin.Context, aliasOrTemplateID api.TemplateID, createBackwardCompatAlias bool) *templateUpdateResult {
	ctx := c.Request.Context()

	body, err := utils.ParseBody[api.TemplateUpdateRequest](ctx, c)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Invalid request body: %s", err))

		return nil
	}

	identifier, _, err := id.ParseName(aliasOrTemplateID)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Invalid template ID: %s", err))
		telemetry.ReportCriticalError(ctx, "invalid template ID", err)

		return nil
	}

	// Resolve template and get the owning team
	team, aliasInfo, apiErr := a.resolveTemplateAndTeam(ctx, c, identifier)
	if apiErr != nil {
		a.sendAPIStoreError(c, apiErr.Code, apiErr.ClientMsg)
		if apiErr.Code != http.StatusNotFound {
			telemetry.ReportCriticalError(ctx, "error resolving template", apiErr.Err)
		}

		return nil
	}

	telemetry.SetAttributes(ctx,
		attribute.String("env.team.id", team.ID.String()),
		attribute.String("env.team.name", team.Name),
		attribute.String("package_version", c.Request.Header.Get("package_version")),
		attribute.Bool("create_backward_compat_alias", createBackwardCompatAlias),
		telemetry.WithTemplateID(aliasInfo.TemplateID),
	)

	// Update template
	if body.Public != nil {
		_, err := a.sqlcDB.UpdateTemplate(ctx, queries.UpdateTemplateParams{
			TemplateIDOrAlias: aliasInfo.TemplateID,
			TeamID:            team.ID,
			Public:            *body.Public,
		})
		if err != nil {
			if dberrors.IsNotFoundError(err) {
				a.sendAPIStoreError(c, http.StatusNotFound, fmt.Sprintf("Template '%s' not found or you don't have access to it", aliasOrTemplateID))
				telemetry.ReportError(ctx, "template not found", err, telemetry.WithTemplateID(aliasInfo.TemplateID))

				return nil
			}

			telemetry.ReportError(ctx, "error when updating template", err)
			a.sendAPIStoreError(c, http.StatusInternalServerError, "Error updating template")

			return nil
		}

		// Invalidate cache immediately after successful DB update
		a.templateCache.InvalidateAllTags(aliasInfo.TemplateID)

		// For backward compatibility with older CLIs (v1 endpoint), also create a non-namespaced alias
		// when publishing a template, so older CLIs can still find it by bare alias name
		if createBackwardCompatAlias && *body.Public {
			if apiErr := a.createBackwardCompatibleAlias(ctx, identifier, aliasInfo.TemplateID, team.Slug); apiErr != nil {
				a.sendAPIStoreError(c, apiErr.Code, apiErr.ClientMsg)
				if apiErr.Err != nil {
					telemetry.ReportError(ctx, "error creating backward compatible alias", apiErr.Err)
				}

				return nil
			}
		}
	}

	telemetry.ReportEvent(ctx, "updated template")

	properties := a.posthog.GetPackageToPosthogProperties(&c.Request.Header)
	a.posthog.IdentifyAnalyticsTeam(ctx, team.ID.String(), team.Name)
	a.posthog.CreateAnalyticsTeamEvent(ctx, team.ID.String(), "updated environment", properties.Set("environment", aliasInfo.TemplateID))

	logger.L().Info(ctx, "Updated template", logger.WithTemplateID(aliasInfo.TemplateID), logger.WithTeamID(team.ID.String()))

	template, err := a.sqlcDB.GetTemplateByIDWithAliases(ctx, aliasInfo.TemplateID)
	if err != nil {
		telemetry.ReportError(ctx, "error getting template names after update", err)
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error retrieving template after update")

		return nil
	}

	return &templateUpdateResult{
		Names: template.Names,
	}
}

// createBackwardCompatibleAlias creates a non-namespaced alias for older CLIs
// that don't support namespace-prefixed template names.
// Uses atomic upsert to avoid race conditions.
func (a *APIStore) createBackwardCompatibleAlias(
	ctx context.Context,
	identifier string,
	templateID string,
	teamSlug string,
) *api.APIError {
	alias := id.ExtractAlias(identifier)
	namespacedName := id.WithNamespace(teamSlug, alias)

	// Atomically try to create the alias or get the existing owner
	upsertedTemplateID, err := a.sqlcDB.UpsertTemplateAliasIfNotExists(ctx, queries.UpsertTemplateAliasIfNotExistsParams{
		Alias:      alias,
		TemplateID: templateID,
		Namespace:  nil,
	})
	if err != nil {
		return &api.APIError{
			Code:      http.StatusInternalServerError,
			ClientMsg: "Error creating backward compatible alias",
			Err:       err,
		}
	}

	// Check if the alias belongs to this template (either newly created or already existed)
	if upsertedTemplateID != templateID {
		return &api.APIError{
			Code: http.StatusConflict,
			ClientMsg: fmt.Sprintf(
				"Public template name '%s' is already taken. Your template is available at '%s'. Please update your CLI to remove this error message.",
				alias, namespacedName),
			Err: nil,
		}
	}

	a.templateCache.InvalidateAlias(nil, alias)
	logger.L().Info(ctx, "Created or verified backward compatible non-namespaced alias",
		logger.WithTemplateID(templateID),
		zap.String("alias", alias))

	return nil
}
