package handlers

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	api "github.com/e2b-dev/infra/packages/shared/pkg/http/hyperloop"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
)

const CollectorExporterTimeout = 10 * time.Second

type APIStore struct {
	logger    *zap.Logger
	sandboxes *smap.Map[*sandbox.Sandbox]

	collectorClient http.Client
	collectorAddr   string
}

func NewHyperloopStore(logger *zap.Logger, sandboxes *smap.Map[*sandbox.Sandbox], sandboxCollectorAddr string) *APIStore {
	return &APIStore{
		logger:    logger,
		sandboxes: sandboxes,

		collectorAddr: sandboxCollectorAddr,
		collectorClient: http.Client{
			Timeout: CollectorExporterTimeout,
		},
	}
}

func (h *APIStore) findSandbox(req *gin.Context) (*sandbox.Sandbox, error) {
	reqIP, _, err := net.SplitHostPort(req.Request.RemoteAddr)
	if err != nil {
		return nil, fmt.Errorf("error parsing remote address %s: %w", req.Request.RemoteAddr, err)
	}

	for _, sbx := range h.sandboxes.Items() {
		if sbx.Slot.HostIPString() == reqIP {
			return sbx, nil
		}
	}

	return nil, fmt.Errorf("sandbox with IP %s not found", reqIP)
}

func (h *APIStore) sendAPIStoreError(c *gin.Context, code int, message string) {
	apiErr := api.Error{
		Code:    int32(code),
		Message: message,
	}

	c.Error(errors.New(message))
	c.JSON(code, apiErr)
}
