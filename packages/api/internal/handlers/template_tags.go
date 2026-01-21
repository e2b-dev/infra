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

// PostTemplatesTags assigns tags to a template build
// The target template is specified in the request body via the "target" field
func (a *APIStore) PostTemplatesTags(c *gin.Context) {
	ctx := c.Request.Context()

	body, err := utils.ParseBody[api.AssignTemplateTagRequest](ctx, c)
	if err != nil {
		a.sendAPIStoreError(c, ctx, http.StatusBadRequest, fmt.Sprintf("Invalid request body: %s", err), err)

		return
	}

	// Parse the target template (alias:tag format)
	targetAlias, targetTag, err := id.ParseTemplateIDOrAliasWithTag(body.Target)
	if err != nil {
		a.sendAPIStoreError(c, ctx, http.StatusBadRequest, fmt.Sprintf("Invalid target template format: %s", body.Target), err)

		return
	}

	// Validate names
	if len(body.Names) == 0 {
		a.sendAPIStoreError(c, ctx, http.StatusBadRequest, "At least one name is required", nil)

		return
	}

	client, tx, err := a.sqlcDB.WithTx(ctx)
	if err != nil {
		a.sendAPIStoreError(c, ctx, http.StatusInternalServerError, "Error beginning transaction", err)

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
			a.sendAPIStoreError(c, ctx, http.StatusNotFound, fmt.Sprintf("Template '%s' not found", body.Target), err)

			return
		}

		a.sendAPIStoreError(c, ctx, http.StatusInternalServerError, "Error getting template", err)

		return
	}

	template := result.Env
	aliases := slices.Concat(result.Aliases, []string{template.ID})
	buildID := result.EnvBuild.ID

	// Get and verify team access
	team, apiErr := a.GetTeam(ctx, c, sharedUtils.ToPtr(template.TeamID.String()))
	if apiErr != nil {
		a.sendAPIStoreError(c, ctx, apiErr.Code, apiErr.ClientMsg, apiErr.Err)

		return
	}

	telemetry.SetAttributesWithGin(c, ctx,
		attribute.String("env.team.id", team.ID.String()),
		attribute.String("env.team.name", team.Name),
		telemetry.WithTemplateID(template.ID),
	)

	if template.TeamID != team.ID {
		a.sendAPIStoreError(c, ctx, http.StatusForbidden, fmt.Sprintf("You don't have access to sandbox template '%s'", targetAlias), nil)

		return
	}

	// Parse tags from body
	tags := make(map[string]bool)
	for _, name := range body.Names {
		alias, tagPtr, err := id.ParseTemplateIDOrAliasWithTag(name)
		if err != nil {
			a.sendAPIStoreError(c, ctx, http.StatusBadRequest, fmt.Sprintf("Invalid name: %s", name), err)

			return
		}

		if !slices.Contains(aliases, alias) {
			a.sendAPIStoreError(c, ctx, http.StatusBadRequest, fmt.Sprintf("Template alias '%s' does not match the target template", alias), nil)

			return
		}

		tag := sharedUtils.DerefOrDefault(tagPtr, id.DefaultTag)

		err = id.ValidateCreateTag(tag)
		if err != nil {
			a.sendAPIStoreError(c, ctx, http.StatusBadRequest, fmt.Sprintf("Invalid tag: %s", err), err)

			return
		}

		tags[tag] = true
	}

	// Create the tag assignments
	for tag := range tags {
		err = client.CreateTemplateBuildAssignment(ctx, queries.CreateTemplateBuildAssignmentParams{
			TemplateID: template.ID,
			BuildID:    buildID,
			Tag:        tag,
		})
		if err != nil {
			a.sendAPIStoreError(c, ctx, http.StatusInternalServerError, "Error creating tag assignment", err)

			return
		}
	}

	err = tx.Commit(ctx)
	if err != nil {
		a.sendAPIStoreError(c, ctx, http.StatusInternalServerError, "Error committing transaction", err)

		return
	}

	// Invalidate the template cache for the new tags
	for tag := range tags {
		a.templateCache.Invalidate(template.ID, &tag)
	}

	telemetry.ReportEvent(ctx, "assigned template tag")

	properties := a.posthog.GetPackageToPosthogProperties(&c.Request.Header)
	a.posthog.IdentifyAnalyticsTeam(ctx, team.ID.String(), team.Name)
	a.posthog.CreateAnalyticsTeamEvent(ctx, team.ID.String(), "assigned template tag",
		properties.
			Set("environment", template.ID).
			Set("tags", slices.Collect(maps.Keys(tags))),
	)

	logger.L().Info(ctx, "Assigned template tag",
		logger.WithTemplateID(template.ID),
		logger.WithTeamID(team.ID.String()),
	)

	c.JSON(http.StatusCreated, api.TemplateTag{
		Names: sharedUtils.Map(slices.Collect(maps.Keys(tags)), func(tag string) string {
			return fmt.Sprintf("%s:%s", targetAlias, tag)
		}),
		BuildID: buildID,
	})
}

// DeleteTemplatesTagsName deletes a tag from a template
// The {name} path parameter is the template:tag to delete (e.g., "web-server:production")
func (a *APIStore) DeleteTemplatesTagsName(c *gin.Context, name string) {
	ctx := c.Request.Context()

	// Parse the name (alias:tag format)
	alias, tagPtr, err := id.ParseTemplateIDOrAliasWithTag(name)
	if err != nil {
		a.sendAPIStoreError(c, ctx, http.StatusBadRequest, fmt.Sprintf("Invalid name format: %s", name), err)

		return
	}

	tag := sharedUtils.DerefOrDefault(tagPtr, id.DefaultTag)

	// Prevent deleting the default tag
	if tag == id.DefaultTag {
		a.sendAPIStoreError(c, ctx, http.StatusBadRequest, fmt.Sprintf("Cannot delete the '%s' tag", id.DefaultTag), nil)

		return
	}

	// Get the template to verify ownership
	template, err := a.sqlcDB.GetTemplateByIdOrAlias(ctx, alias)
	if err != nil {
		if dberrors.IsNotFoundError(err) {
			a.sendAPIStoreError(c, ctx, http.StatusNotFound, fmt.Sprintf("Template '%s' not found", alias), nil)

			return
		}

		a.sendAPIStoreError(c, ctx, http.StatusInternalServerError, "Error getting template", nil)

		return
	}

	// Get and verify team access
	team, apiErr := a.GetTeam(ctx, c, sharedUtils.ToPtr(template.TeamID.String()))
	if apiErr != nil {
		a.sendAPIStoreError(c, ctx, apiErr.Code, apiErr.ClientMsg, apiErr.Err)

		return
	}

	telemetry.SetAttributesWithGin(c, ctx,
		attribute.String("env.team.id", team.ID.String()),
		attribute.String("env.team.name", team.Name),
		telemetry.WithTemplateID(template.ID),
	)

	if template.TeamID != team.ID {
		a.sendAPIStoreError(c, ctx, http.StatusForbidden, fmt.Sprintf("You don't have access to sandbox template '%s'", alias), nil)

		return
	}

	// Delete the tag assignment
	err = a.sqlcDB.DeleteTemplateTag(ctx, queries.DeleteTemplateTagParams{
		TemplateID: template.ID,
		Tag:        tag,
	})
	if err != nil {
		a.sendAPIStoreError(c, ctx, http.StatusInternalServerError, "Error deleting tag assignment", err)

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
