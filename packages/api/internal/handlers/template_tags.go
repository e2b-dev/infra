package handlers

import (
	"fmt"
	"maps"
	"net/http"
	"slices"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/attribute"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/db/dberrors"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/id"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	sharedUtils "github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

// PostTemplatesTemplateIDTags assigns tags to a template build
func (a *APIStore) PostTemplatesTemplateIDTagsTag(c *gin.Context, templateAlias api.TemplateID, tag string) {
	ctx := c.Request.Context()

	body, err := utils.ParseBody[api.AssignTemplateTagRequest](ctx, c)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Invalid request body: %s", err))

		return
	}

	// Validate tag name
	if len(body.Names) == 0 {
		a.sendAPIStoreError(c, http.StatusBadRequest, "At least one name is required")
		telemetry.ReportError(ctx, "at least one name is required", nil)

		return
	}

	// Get template and build from the source tag in a single query
	result, err := a.sqlcDB.GetTemplateWithBuildByTag(ctx, queries.GetTemplateWithBuildByTagParams{
		AliasOrEnvID: templateAlias,
		Tag:          &tag,
	})
	if err != nil {
		if dberrors.IsNotFoundError(err) {
			a.sendAPIStoreError(c, http.StatusNotFound, fmt.Sprintf("Template '%s' with tag '%s' not found", templateAlias, tag))
			telemetry.ReportError(ctx, "template or source tag not found", err, telemetry.WithTemplateID(templateAlias))

			return
		}

		telemetry.ReportError(ctx, "error when getting template with build", err)
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error getting template")

		return
	}

	template := result.Env
	aliases := slices.Concat(result.Aliases, []string{template.ID})
	buildID := result.EnvBuild.ID

	// Get and verify team access
	team, apiErr := a.GetTeam(ctx, c, sharedUtils.ToPtr(template.TeamID.String()))
	if apiErr != nil {
		a.sendAPIStoreError(c, apiErr.Code, apiErr.ClientMsg)
		telemetry.ReportCriticalError(ctx, "error when getting team", apiErr.Err)

		return
	}

	telemetry.SetAttributes(ctx,
		attribute.String("env.team.id", team.ID.String()),
		attribute.String("env.team.name", team.Name),
		telemetry.WithTemplateID(template.ID),
	)

	if template.TeamID != team.ID {
		a.sendAPIStoreError(c, http.StatusForbidden, fmt.Sprintf("You don't have access to sandbox template '%s'", templateAlias))
		telemetry.ReportError(ctx, "no access to the template", nil, telemetry.WithTemplateID(template.ID))

		return
	}

	tags := make(map[string]bool)
	for _, name := range body.Names {
		alias, tag, err := id.ParseTemplateIDOrAliasWithTag(name)
		if err != nil {
			a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Invalid tag name: %s", name))
			telemetry.ReportCriticalError(ctx, "invalid tag name", err)

			return
		}

		if !slices.Contains(aliases, alias) {
			a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Template alias '%s' not matching the template", alias))
			telemetry.ReportCriticalError(ctx, "template alias not matching the template", err)

			return
		}

		tags[sharedUtils.DerefOrDefault(tag, id.DefaultTag)] = true
	}

	// Create the tag assignment
	for tag := range tags {
		err = a.sqlcDB.CreateTemplateBuildAssignment(ctx, queries.CreateTemplateBuildAssignmentParams{
			TemplateID: template.ID,
			BuildID:    buildID,
			Tag:        tag,
		})
		if err != nil {
			telemetry.ReportCriticalError(ctx, "error when creating tag assignment", err)
			a.sendAPIStoreError(c, http.StatusInternalServerError, "Error creating tag assignment")

			return
		}
	}

	// Invalidate the template cache for the new tag
	for tag := range tags {
		a.templateCache.Invalidate(template.ID, &tag)
	}

	telemetry.ReportEvent(ctx, "assigned template tag")

	properties := a.posthog.GetPackageToPosthogProperties(&c.Request.Header)
	a.posthog.IdentifyAnalyticsTeam(ctx, team.ID.String(), team.Name)
	a.posthog.CreateAnalyticsTeamEvent(ctx, team.ID.String(), "assigned template tag",
		properties.
			Set("environment", template.ID).
			Set("tags", maps.Keys(tags)),
	)

	logger.L().Info(ctx, "Assigned template tag",
		logger.WithTemplateID(template.ID),
		logger.WithTeamID(team.ID.String()),
	)

	c.JSON(http.StatusCreated, api.TemplateTag{
		Tags:    slices.Collect(maps.Keys(tags)),
		BuildID: buildID,
	})
}

// DeleteTemplatesTemplateIDTagsTag deletes a tag from a template
func (a *APIStore) DeleteTemplatesTemplateIDTagsTag(c *gin.Context, templateIDOrAlias api.TemplateID, tag string) {
	ctx := c.Request.Context()

	// Prevent deleting the default tag
	if tag == id.DefaultTag {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Cannot delete the '%s' tag", id.DefaultTag))
		telemetry.ReportError(ctx, "cannot delete default tag", nil)

		return
	}

	// Get the template to verify ownership
	template, err := a.sqlcDB.GetTemplateByIdOrAlias(ctx, templateIDOrAlias)
	if err != nil {
		if dberrors.IsNotFoundError(err) {
			a.sendAPIStoreError(c, http.StatusNotFound, fmt.Sprintf("Template '%s' not found", templateIDOrAlias))
			telemetry.ReportError(ctx, "template not found", err, telemetry.WithTemplateID(templateIDOrAlias))

			return
		}

		telemetry.ReportError(ctx, "error when getting template", err)
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error getting template")

		return
	}

	// Get and verify team access
	team, apiErr := a.GetTeam(ctx, c, sharedUtils.ToPtr(template.TeamID.String()))
	if apiErr != nil {
		a.sendAPIStoreError(c, apiErr.Code, apiErr.ClientMsg)
		telemetry.ReportCriticalError(ctx, "error when getting team", apiErr.Err)

		return
	}

	telemetry.SetAttributes(ctx,
		attribute.String("env.team.id", team.ID.String()),
		attribute.String("env.team.name", team.Name),
		telemetry.WithTemplateID(template.ID),
	)

	if template.TeamID != team.ID {
		a.sendAPIStoreError(c, http.StatusForbidden, fmt.Sprintf("You don't have access to sandbox template '%s'", templateIDOrAlias))
		telemetry.ReportError(ctx, "no access to the template", nil, telemetry.WithTemplateID(template.ID))

		return
	}

	// Delete the tag assignment
	err = a.sqlcDB.DeleteTemplateBuildAssignment(ctx, queries.DeleteTemplateBuildAssignmentParams{
		TemplateID: template.ID,
		Tag:        tag,
	})
	if err != nil {
		telemetry.ReportCriticalError(ctx, "error when deleting tag assignment", err)
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error deleting tag assignment")

		return
	}

	// Invalidate the template cache for the deleted tag
	a.templateCache.Invalidate(template.ID, &tag)

	telemetry.ReportEvent(ctx, "deleted template tag")

	properties := a.posthog.GetPackageToPosthogProperties(&c.Request.Header)
	a.posthog.IdentifyAnalyticsTeam(ctx, team.ID.String(), team.Name)
	a.posthog.CreateAnalyticsTeamEvent(ctx, team.ID.String(), "deleted template tag", properties.Set("environment", template.ID).Set("tag", tag))

	logger.L().Info(ctx, "Deleted template tag",
		logger.WithTemplateID(template.ID),
		logger.WithTeamID(team.ID.String()),
	)

	c.Status(http.StatusNoContent)
}
