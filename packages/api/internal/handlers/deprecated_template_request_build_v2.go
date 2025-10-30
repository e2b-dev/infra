package handlers

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	apiutils "github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

// PostV2Templates triggers a new template build
func (a *APIStore) PostV2Templates(ctx *gin.Context) {
	body, err := apiutils.ParseBody[api.TemplateBuildRequestV2](ctx, ctx)
	if err != nil {
		a.sendAPIStoreError(ctx, http.StatusBadRequest, fmt.Sprintf("Invalid request body: %s", err))
		telemetry.ReportCriticalError(ctx, "invalid request body", err)

		return
	}

	t := requestTemplateBuild(ctx, a, api.TemplateBuildRequestV3(body))
	if t != nil {
		ctx.JSON(http.StatusAccepted, &api.TemplateLegacy{
			TemplateID: t.TemplateID,
			BuildID:    t.BuildID,
			Public:     t.Public,
			Aliases:    t.Aliases,
		})
	}
}
