package handlers

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/cfg"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/apierrors"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

const CollectorExporterTimeout = 10 * time.Second

type APIStore struct {
	logger    logger.Logger
	sandboxes *sandbox.Map

	collectorClient        http.Client
	collectorAddr          string
	writeEnvdMessagesToLog bool
}

func NewHyperloopStore(logger logger.Logger, sandboxes *sandbox.Map, config cfg.Config) *APIStore {
	return &APIStore{
		logger:    logger,
		sandboxes: sandboxes,

		writeEnvdMessagesToLog: config.WriteEnvdMessagesToLog,
		collectorAddr:          config.LogsCollectorAddress,
		collectorClient: http.Client{
			Timeout: CollectorExporterTimeout,
		},
	}
}

func (h *APIStore) sendAPIStoreError(c *gin.Context, code int, message string) {
	apierrors.SendAPIStoreError(c, code, message)
}
