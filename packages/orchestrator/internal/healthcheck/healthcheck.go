package healthcheck

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/service"
	e2borchestratorinfo "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator-info"
	e2bHealth "github.com/e2b-dev/infra/packages/shared/pkg/health"
)

type Healthcheck struct {
	info *service.ServiceInfo

	lastRun time.Time
	mu      sync.RWMutex
}

func NewHealthcheck(info *service.ServiceInfo) (*Healthcheck, error) {
	return &Healthcheck{
		info: info,

		lastRun: time.Now(),
		mu:      sync.RWMutex{},
	}, nil
}

func (h *Healthcheck) CreateHandler() http.Handler {
	// Start /health HTTP server
	routeMux := http.NewServeMux()
	routeMux.HandleFunc("/health", h.healthHandler)
	return routeMux
}

func (h *Healthcheck) getStatus() e2bHealth.Status {
	switch h.info.GetStatus() {
	case e2borchestratorinfo.ServiceInfoStatus_Healthy:
		return e2bHealth.Healthy
	case e2borchestratorinfo.ServiceInfoStatus_Draining:
		return e2bHealth.Draining
	}

	return e2bHealth.Unhealthy
}

func (h *Healthcheck) healthHandler(w http.ResponseWriter, _ *http.Request) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	status := h.getStatus()
	response := e2bHealth.Response{Status: status, Version: h.info.SourceCommit}

	w.Header().Set("Content-Type", "application/json")
	if status == e2bHealth.Unhealthy {
		w.WriteHeader(http.StatusServiceUnavailable)
	} else {
		w.WriteHeader(http.StatusOK)
	}

	if err := json.NewEncoder(w).Encode(response); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
