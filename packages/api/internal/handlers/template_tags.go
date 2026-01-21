package handlers

import (
	"fmt"
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

// PostTemplatesTags assigns tags to a template build
// The target template is specified in the request body via the "target" field
func (a *APIStore) PostTemplatesTags(c *gin.Context) {
	ctx := c.Request.Context()

	body, err := utils.ParseBody[api.AssignTemplateTagsRequest](ctx, c)
	if err != nil {
		a.sendAPIStoreError(ctx, c, http.StatusBadRequest, fmt.Sprintf("Invalid request body: %s", err), err)

		return
	}

	// Parse the target template (name:tag format)
	targetAlias, targetTag, err := id.ParseTemplateIDOrAliasWithTag(body.Target)
	if err != nil {
		a.sendAPIStoreError(ctx, c, http.StatusBadRequest, fmt.Sprintf("Invalid target template format: %s", body.Target), err)

		return
	}

	// Validate tags
	if len(body.Tags) == 0 {
		a.sendAPIStoreError(ctx, c, http.StatusBadRequest, "At least one tag is required", nil)

		return
	}

	client, tx, err := a.sqlcDB.WithTx(ctx)
	if err != nil {
		a.sendAPIStoreError(ctx, c, http.StatusInternalServerError, "Error beginning transaction", err)

		return
	}
	defer tx.Rollback(ctx)

	// Get template and build from the target tag
	targetTagValue := sharedUtils.DerefOrDefault(targetTag, id.DefaultTag)
	result, err := client.GetTemplateWithBuildByTag(ctx, queries.GetTemplateWithBuildByTagParams{
		AliasOrEnvID: targetAlias,
		Tag:          &targetTagValue,
	})
	if err != nil {
		if dberrors.IsNotFoundError(err) {
			a.sendAPIStoreError(ctx, c, http.StatusNotFound, fmt.Sprintf("Template '%s' not found", body.Target), err)

			return
		}

		a.sendAPIStoreError(ctx, c, http.StatusInternalServerError, "Error getting template", err)

		return
	}

	template := result.Env
	buildID := result.EnvBuild.ID

	// Get and verify team access
	team, apiErr := a.GetTeam(ctx, c, sharedUtils.ToPtr(template.TeamID.String()))
	if apiErr != nil {
		a.sendAPIStoreError(ctx, c, apiErr.Code, apiErr.ClientMsg, apiErr.Err)

		return
	}

	ctx = telemetry.WithAttributes(ctx,
		attribute.String("env.team.id", team.ID.String()),
		attribute.String("env.team.name", team.Name),
		telemetry.WithTemplateID(template.ID),
	)

	if template.TeamID != team.ID {
		a.sendAPIStoreError(ctx, c, http.StatusForbidden, fmt.Sprintf("You don't have access to sandbox template '%s'", targetAlias), nil)

		return
	}

	tags, err := id.ValidateAndDeduplicateTags(body.Tags)
	if err != nil {
		a.sendAPIStoreError(ctx, c, http.StatusBadRequest, fmt.Sprintf("Invalid tag: %s", err), err)

		return
	}

	// Create the tag assignments
	for _, tag := range tags {
		err = client.CreateTemplateBuildAssignment(ctx, queries.CreateTemplateBuildAssignmentParams{
			TemplateID: template.ID,
			BuildID:    buildID,
			Tag:        tag,
		})
		if err != nil {
			a.sendAPIStoreError(ctx, c, http.StatusInternalServerError, "Error creating tag assignment", err)

			return
		}
	}

	err = tx.Commit(ctx)
	if err != nil {
		a.sendAPIStoreError(ctx, c, http.StatusInternalServerError, "Error committing transaction", err)

		return
	}

	// Invalidate the template cache for the new tags
	for _, tag := range tags {
		a.templateCache.Invalidate(template.ID, &tag)
	}

	telemetry.ReportEvent(ctx, "assigned template tag")

	properties := a.posthog.GetPackageToPosthogProperties(&c.Request.Header)
	a.posthog.IdentifyAnalyticsTeam(ctx, team.ID.String(), team.Name)
	a.posthog.CreateAnalyticsTeamEvent(ctx, team.ID.String(), "assigned template tag",
		properties.
			Set("environment", template.ID).
			Set("tags", tags),
	)

	logger.L().Info(ctx, "Assigned template tag",
		logger.WithTemplateID(template.ID),
		logger.WithTeamID(team.ID.String()),
	)

	c.JSON(http.StatusCreated, api.AssignedTemplateTags{
		Tags:    tags,
		BuildID: buildID,
	})
}

// DeleteTemplatesTags deletes multiple tags from a template
func (a *APIStore) DeleteTemplatesTags(c *gin.Context) {
	ctx := c.Request.Context()

	body, err := utils.ParseBody[api.DeleteTemplateTagsRequest](ctx, c)
	if err != nil {
		a.sendAPIStoreError(ctx, c, http.StatusBadRequest, fmt.Sprintf("Invalid request body: %s", err), err)

		return
	}

	// Parse and validate the template name
	templateName, tagFromName, err := id.ParseTemplateIDOrAliasWithTag(body.Name)
	if err != nil {
		a.sendAPIStoreError(ctx, c, http.StatusBadRequest, fmt.Sprintf("Invalid template name format: %s", body.Name), err)
		telemetry.ReportError(ctx, "invalid template name format", err)

		return
	}

	// Reject if name contains a tag - tags should be specified in the tags field
	if tagFromName != nil {
		a.sendAPIStoreError(ctx, c, http.StatusBadRequest, "Template name should not contain a tag, use the 'tags' field instead", nil)

		return
	}

	// Validate and normalize tags
	tags, err := id.ValidateAndDeduplicateTags(body.Tags)
	if err != nil {
		a.sendAPIStoreError(ctx, c, http.StatusBadRequest, fmt.Sprintf("Invalid tag: %s", err), err)

		return
	}

	// Validate tags
	if len(tags) == 0 {
		a.sendAPIStoreError(ctx, c, http.StatusBadRequest, "At least one tag is required", nil)

		return
	}

	// Validate that no tag is the default tag
	if slices.Contains(tags, id.DefaultTag) {
		a.sendAPIStoreError(ctx, c, http.StatusBadRequest, fmt.Sprintf("Cannot delete the '%s' tag", id.DefaultTag), nil)

		return
	}

	// Get the template to verify ownership
	template, err := a.sqlcDB.GetTemplateByIdOrAlias(ctx, templateName)
	if err != nil {
		if dberrors.IsNotFoundError(err) {
			a.sendAPIStoreError(ctx, c, http.StatusNotFound, fmt.Sprintf("Template '%s' not found", templateName), err)

			return
		}

		a.sendAPIStoreError(ctx, c, http.StatusInternalServerError, "Error getting template", err)

		return
	}

	// Get and verify team access
	team, apiErr := a.GetTeam(ctx, c, sharedUtils.ToPtr(template.TeamID.String()))
	if apiErr != nil {
		a.sendAPIStoreError(ctx, c, apiErr.Code, apiErr.ClientMsg, apiErr.Err)

		return
	}

	if template.TeamID != team.ID {
		a.sendAPIStoreError(ctx, c, http.StatusForbidden, fmt.Sprintf("You don't have access to sandbox template '%s'", templateName), nil)

		return
	}

	telemetry.WithAttributes(ctx,
		attribute.String("env.team.id", team.ID.String()),
		attribute.String("env.team.name", team.Name),
		telemetry.WithTemplateID(template.ID),
	)

	// Delete the tag assignments
	err = a.sqlcDB.DeleteTemplateTags(ctx, queries.DeleteTemplateTagsParams{
		TemplateID: template.ID,
		Tags:       tags,
	})
	if err != nil {
		a.sendAPIStoreError(ctx, c, http.StatusInternalServerError, "Error deleting tag assignments", err)

		return
	}

	// Invalidate the template cache for the deleted tags
	for _, tag := range tags {
		a.templateCache.Invalidate(template.ID, &tag)
	}

	telemetry.ReportEvent(ctx, "deleted template tags")

	properties := a.posthog.GetPackageToPosthogProperties(&c.Request.Header)
	a.posthog.IdentifyAnalyticsTeam(ctx, team.ID.String(), team.Name)
	a.posthog.CreateAnalyticsTeamEvent(ctx, team.ID.String(), "deleted template tags", properties.Set("environment", template.ID).Set("tags", tags))

	logger.L().Info(ctx, "Deleted template tags",
		logger.WithTemplateID(template.ID),
		logger.WithTeamID(team.ID.String()),
	)

	c.Status(http.StatusNoContent)
}
