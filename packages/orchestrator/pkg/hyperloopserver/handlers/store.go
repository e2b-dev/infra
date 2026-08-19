//go:build linux

package handlers

import (
	"net/http"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/apierrors"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	sbxlogger "github.com/e2b-dev/infra/packages/shared/pkg/logger/sandbox"
)

const CollectorExporterTimeout = 10 * time.Second

type APIStore struct {
	logger    logger.Logger
	sandboxes *sandbox.Map

	collectorClient       http.Client
	sandboxCollectorAddr  string
	sandboxClickhouseAddr string
	shadowEnabled         bool
	shadowInflight        atomic.Int64
}

func NewHyperloopStore(logger logger.Logger, sandboxes *sandbox.Map, sandboxCollectorAddr string, writeOnly bool) *APIStore {
	return &APIStore{
		logger:    logger,
		sandboxes: sandboxes,

		collectorClient: http.Client{
			Timeout: CollectorExporterTimeout,
		},
		sandboxCollectorAddr:  sbxlogger.ExternalLogEndpoint(sandboxCollectorAddr, writeOnly),
		sandboxClickhouseAddr: sbxlogger.ClickhouseLogsWriteEndpoint,
		shadowEnabled:         !writeOnly,
		shadowInflight:        atomic.Int64{},
	}
}

func (h *APIStore) sendAPIStoreError(c *gin.Context, code int, message string) {
	apierrors.SendAPIStoreError(c, code, message)
}
