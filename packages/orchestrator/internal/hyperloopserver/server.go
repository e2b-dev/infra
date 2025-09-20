package hyperloopserver

import (
	"context"
	"fmt"
	"net"
	"net/http"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
)

type HyperloopStore struct {
	server    *http.Server
	sandboxes *smap.Map[*sandbox.Sandbox]
}

func NewHyperloopServer(port uint, sandboxes *smap.Map[*sandbox.Sandbox]) (*HyperloopStore, error) {
	store := &HyperloopStore{sandboxes: sandboxes}

	mux := http.NewServeMux()
	mux.HandleFunc("/", store.handler)

	store.server = &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: mux,
	}

	return store, nil
}

func (h *HyperloopStore) Start() error {
	return h.server.ListenAndServe()
}

func (h *HyperloopStore) Close(ctx context.Context) error {
	return h.server.Shutdown(ctx)
}

func (h *HyperloopStore) handler(w http.ResponseWriter, r *http.Request) {
	sbx, err := h.findSandbox(r)
	if err != nil {
		zap.L().Error("Error finding sandbox for hyperloop request", zap.Error(err))
		http.Error(w, "Sandbox IPv4 not found", http.StatusNotFound)
		return
	}

	response := fmt.Sprintf("Responding to sandbox %s", sbx.Runtime.SandboxID)
	w.Write([]byte(response))
}

func (h *HyperloopStore) findSandbox(req *http.Request) (*sandbox.Sandbox, error) {
	reqIP, _, err := net.SplitHostPort(req.RemoteAddr)
	if err != nil {
		return nil, fmt.Errorf("error parsing remote address %s: %w", req.RemoteAddr, err)
	}

	for _, sbx := range h.sandboxes.Items() {
		if sbx.Slot.HostIPString() == reqIP {
			return sbx, nil
		}
	}

	return nil, fmt.Errorf("sandbox with IP %s not found", reqIP)
}
