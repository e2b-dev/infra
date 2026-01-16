package handlers

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	api "github.com/e2b-dev/infra/packages/shared/pkg/http/hyperloop"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

const CollectorExporterTimeout = 10 * time.Second

type APIStore struct {
	logger    logger.Logger
	sandboxes *sandbox.Map

	collectorClient http.Client
	collectorAddr   string
}

func NewHyperloopStore(logger logger.Logger, sandboxes *sandbox.Map, sandboxCollectorAddr string) *APIStore {
	return &APIStore{
		logger:    logger,
		sandboxes: sandboxes,

		collectorAddr: sandboxCollectorAddr,
		collectorClient: http.Client{
			Timeout: CollectorExporterTimeout,
		},
	}
}

func (h *APIStore) sendAPIStoreError(c *gin.Context, code int, message string) {
	apiErr := api.Error{
		Code:    int32(code),
		Message: message,
	}

	_ = c.Error(errors.New(message))
	c.JSON(code, apiErr)
}
