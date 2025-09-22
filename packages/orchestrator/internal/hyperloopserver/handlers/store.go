package handlers

import (
	"fmt"
	"net"
	"net/http"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type APIStore struct {
	logger    *zap.Logger
	sandboxes *smap.Map[*sandbox.Sandbox]
}

func NewHyperloopStore(logger *zap.Logger, sandboxes *smap.Map[*sandbox.Sandbox]) *APIStore {
	return &APIStore{
		logger:    logger,
		sandboxes: sandboxes,
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

func (h *APIStore) Logger(c *gin.Context) {
	println("Received hyperloop request from", c.Request.RemoteAddr)

	sbx, err := h.findSandbox(c)
	if err != nil {
		zap.L().Error("Error finding sandbox for hyperloop request", zap.Error(err))
		http.Error(c.Writer, "Sandbox IPv4 not found", http.StatusNotFound)
		return
	}

	println("Found sandbox", sbx.Runtime.SandboxID, "for request from", c.Request.RemoteAddr)

	response := fmt.Sprintf("Responding to sandbox %s", sbx.Runtime.SandboxID)
	c.Writer.Write([]byte(response))
}

func (h *APIStore) Me(c *gin.Context) {
	//TODO implement me
	panic("implement me")
}
