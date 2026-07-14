//go:build linux

package handlers

import (
	"net/http"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/apierrors"
	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

const CollectorExporterTimeout = 10 * time.Second

const defaultMaxInflightShadowWrites = 1024

type APIStore struct {
	logger    logger.Logger
	sandboxes *sandbox.Map

	collectorClient http.Client
	logWriteConfig  *featureflags.LogWriteConfigResolver

	// shadowInflight bounds concurrent shadow forwards (best-effort, non-blocking
	// acquire; dropped when the resolved route limit is reached).
	shadowInflight atomic.Int64
}

func NewHyperloopStore(logger logger.Logger, sandboxes *sandbox.Map, sandboxCollectorAddr string, featureFlags *featureflags.Client) *APIStore {
	return &APIStore{
		logger:    logger,
		sandboxes: sandboxes,

		collectorClient: http.Client{
			Timeout: CollectorExporterTimeout,
		},
		logWriteConfig: featureflags.NewLogWriteConfigResolver(featureFlags, sandboxCollectorAddr),
	}
}

func (h *APIStore) sendAPIStoreError(c *gin.Context, code int, message string) {
	apierrors.SendAPIStoreError(c, code, message)
}
