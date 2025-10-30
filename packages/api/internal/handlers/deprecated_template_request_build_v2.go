package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/api/internal/api"
)

// PostV2Templates triggers a new template build
func (a *APIStore) PostV2Templates(c *gin.Context) {
	t := requestTemplateBuild(a, c)
	if t != nil {
		c.JSON(http.StatusAccepted, &api.TemplateLegacy{
			TemplateID: t.TemplateID,
			BuildID:    t.BuildID,
			Public:     t.Public,
			Aliases:    t.Aliases,
		})
	}
}
