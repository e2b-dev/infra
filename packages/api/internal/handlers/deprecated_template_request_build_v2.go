package handlers

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	apiutils "github.com/e2b-dev/infra/packages/api/internal/utils"
)

// PostV2Templates triggers a new template build
func (a *APIStore) PostV2Templates(c *gin.Context) {
	ctx := c.Request.Context()

	body, err := apiutils.ParseBody[api.TemplateBuildRequestV2](ctx, c)
	if err != nil {
		a.sendAPIStoreError(c, ctx, http.StatusBadRequest, fmt.Sprintf("Invalid request body: %s", err), err)

		return
	}

	t := requestTemplateBuild(ctx, c, a, api.TemplateBuildRequestV3{
		Names:    &[]string{body.Alias},
		CpuCount: body.CpuCount,
		MemoryMB: body.MemoryMB,
		TeamID:   body.TeamID,
	})
	if t != nil {
		c.JSON(http.StatusAccepted, &api.TemplateLegacy{
			TemplateID: t.TemplateID,
			BuildID:    t.BuildID,
			Public:     t.Public,
			Aliases:    t.Aliases,
		})
	}
}
