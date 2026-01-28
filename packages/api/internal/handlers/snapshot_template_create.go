package handlers

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/trace"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	templatecache "github.com/e2b-dev/infra/packages/api/internal/cache/templates"
	"github.com/e2b-dev/infra/packages/api/internal/orchestrator"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/shared/pkg/id"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (a *APIStore) PostSandboxesSandboxIDSnapshots(c *gin.Context, sandboxID api.SandboxID) {
	ctx := c.Request.Context()

	teamInfo := a.GetTeamInfo(c)
	teamID := teamInfo.Team.ID

	span := trace.SpanFromContext(ctx)
	traceID := span.SpanContext().TraceID().String()
	c.Set("traceID", traceID)

	telemetry.SetAttributes(ctx,
		telemetry.WithTeamID(teamID.String()),
		telemetry.WithSandboxID(sandboxID),
	)

	sandboxID = utils.ShortID(sandboxID)

	// Parse optional request body
	var body api.PostSandboxesSandboxIDSnapshotsJSONRequestBody
	_ = c.ShouldBindJSON(&body)

	// Build opts from the optional name
	opts := orchestrator.SnapshotTemplateOpts{
		Tag: id.DefaultTag,
	}

	if body.Name != nil {
		identifier, tag, err := id.ParseName(*body.Name)
		if err != nil {
			a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Invalid name: %s", err))

			return
		}

		if err := id.ValidateNamespaceMatchesTeam(identifier, teamInfo.Slug); err != nil {
			a.sendAPIStoreError(c, http.StatusBadRequest, err.Error())

			return
		}

		alias := id.ExtractAlias(identifier)

		if tag != nil {
			opts.Tag = *tag
		}

		// Resolve alias using the cache — same pattern as template builds
		aliasInfo, err := a.templateCache.ResolveAlias(ctx, identifier, teamInfo.Slug)
		switch {
		case err == nil && aliasInfo.TeamID == teamID:
			// Alias exists and is owned by this team — reuse the template
			opts.ExistingTemplateID = &aliasInfo.TemplateID
		case err == nil || errors.Is(err, templatecache.ErrTemplateNotFound):
			// Not found, or owned by a different team — will create a new template with this alias
		default:
			apiErr := templatecache.ErrorToAPIError(err, identifier)
			a.sendAPIStoreError(c, apiErr.Code, apiErr.ClientMsg)
			telemetry.ReportCriticalError(ctx, "error resolving snapshot template alias", apiErr.Err)

			return
		}

		opts.Alias = &alias
		opts.Namespace = &teamInfo.Slug
	}

	sbx, err := a.orchestrator.GetSandbox(ctx, teamID, sandboxID)
	if err != nil {
		var notFoundErr *sandbox.NotFoundError
		if errors.As(err, &notFoundErr) {
			a.sendAPIStoreError(c, http.StatusNotFound, fmt.Sprintf("Sandbox '%s' not found or not running", sandboxID))

			return
		}
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error getting sandbox")

		return
	}

	if sbx.TeamID != teamID {
		a.sendAPIStoreError(c, http.StatusForbidden, fmt.Sprintf("You don't have access to sandbox '%s'", sandboxID))

		return
	}

	telemetry.ReportEvent(ctx, "Creating snapshot template")

	result, err := a.orchestrator.CreateSnapshotTemplate(ctx, teamID, sandboxID, opts)
	if err != nil {
		if errors.Is(err, orchestrator.ErrSandboxNotRunning) {
			a.sendAPIStoreError(c, http.StatusConflict, fmt.Sprintf("Sandbox '%s' is not running or is already being snapshotted", sandboxID))

			return
		}

		telemetry.ReportCriticalError(ctx, "Error creating snapshot template", err, telemetry.WithSandboxID(sandboxID))
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error creating snapshot template")

		return
	}

	// Invalidate cached tombstone if a new alias was created
	if opts.Alias != nil && opts.Namespace != nil && opts.ExistingTemplateID == nil {
		a.templateCache.InvalidateAlias(opts.Namespace, *opts.Alias)
	}

	// Build the full name: namespace/alias:tag (or just the template ID if no name was provided)
	name := result.TemplateID
	if opts.Alias != nil && opts.Namespace != nil {
		name = id.WithNamespace(*opts.Namespace, *opts.Alias)
		if opts.Tag != id.DefaultTag {
			name += id.TagSeparator + opts.Tag
		}
	}

	c.JSON(http.StatusCreated, api.SnapshotInfo{
		SnapshotID: result.TemplateID,
		Name:       name,
	})
}
