package handlers

import (
	"errors"

	"github.com/gin-gonic/gin"

	templatecache "github.com/e2b-dev/infra/packages/api/internal/cache/templates"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

// templateFilterOutcome tells the caller what resolveTemplateFilter did.
type templateFilterOutcome int

const (
	// templateFilterResolved: the identifier resolved, use the returned template ID.
	templateFilterResolved templateFilterOutcome = iota
	// templateFilterNoMatch: the identifier names no template the caller can see, so
	// a filtered list is necessarily empty. The caller writes its own empty response,
	// because the element type and the response headers differ per endpoint.
	templateFilterNoMatch
	// templateFilterFailed: resolution failed and the error response is already
	// written. The caller must return without writing anything else.
	templateFilterFailed
)

// resolveTemplateFilter resolves a template identifier that is being used to filter a
// list endpoint, and maps the failure modes onto the responses those endpoints share.
//
// The identifier may carry an explicit namespace so a filter can name a public
// template owned by another team. That is safe because resolution only produces an ID
// to compare against: every listing query is still scoped by its own team_id
// predicate, so an identifier that resolves to another team's template yields an empty
// list rather than another team's rows.
//
// telemetryMessage labels the critical error reported for an unexpected failure.
func (a *APIStore) resolveTemplateFilter(c *gin.Context, identifier, teamSlug, telemetryMessage string) (string, templateFilterOutcome) {
	ctx := c.Request.Context()

	aliasInfo, err := a.templateCache.ResolveAlias(ctx, identifier, teamSlug)
	switch {
	case err == nil:
		return aliasInfo.TemplateID, templateFilterResolved
	case errors.Is(err, templatecache.ErrTemplateNotFound):
		return "", templateFilterNoMatch
	default:
		apiErr := templatecache.ErrorToAPIError(err, identifier)
		a.sendAPIStoreError(c, apiErr.Code, apiErr.ClientMsg)
		telemetry.ReportCriticalError(ctx, telemetryMessage, apiErr.Err)

		return "", templateFilterFailed
	}
}
