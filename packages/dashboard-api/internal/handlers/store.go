package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	clickhouse "github.com/e2b-dev/infra/packages/clickhouse/pkg"
	"github.com/e2b-dev/infra/packages/dashboard-api/internal/api"
	sqlcdb "github.com/e2b-dev/infra/packages/db/client"
)

var _ api.ServerInterface = (*APIStore)(nil)

type APIStore struct {
	db         *sqlcdb.Client
	clickhouse clickhouse.Clickhouse
}

func NewAPIStore(db *sqlcdb.Client, ch clickhouse.Clickhouse) *APIStore {
	return &APIStore{
		db:         db,
		clickhouse: ch,
	}
}

func (s *APIStore) GetHealth(c *gin.Context) {
	c.String(http.StatusOK, "Health check successful")
}
