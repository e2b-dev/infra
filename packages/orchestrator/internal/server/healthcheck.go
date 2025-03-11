package server

import (
	"context"
	"encoding/json"
	"log"
	"net"
	"net/http"
	"sync"
	"time"

	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	"github.com/e2b-dev/infra/packages/shared/pkg/meters"
)

const healthcheckFrequency = 5 * time.Second
const healthcheckTimeout = 30 * time.Second

type Status string

const (
	Healthy   Status = "healthy"
	Unhealthy Status = "unhealthy"
)

type Healthcheck struct {
	version    string
	server     *server
	grpc       *grpc.Server
	grpcHealth *health.Server

	// TODO: Replace with status from SQL Lite
	status  Status
	lastRun time.Time
	mu      sync.RWMutex

	sandboxCounter metric.Int64Gauge
}

func NewHealthcheck(server *server, grpc *grpc.Server, grpcHealth *health.Server, version string) (*Healthcheck, error) {
	sandboxCounter, err := meters.GetGauge(meters.OrchestratorSandboxCountMeterName)
	if err != nil {
		return nil, err
	}

	return &Healthcheck{
		version:    version,
		server:     server,
		grpc:       grpc,
		grpcHealth: grpcHealth,

		status:  Unhealthy,
		lastRun: time.Now(),
		mu:      sync.RWMutex{},

		sandboxCounter: sandboxCounter,
	}, nil
}

func (h *Healthcheck) Start(ctx context.Context, listener net.Listener) {
	ticker := time.NewTicker(healthcheckFrequency)
	defer ticker.Stop()

	routeMux := http.NewServeMux()
	routeMux.HandleFunc("/health", h.healthHandler)
	httpServer := &http.Server{
		Handler: routeMux,
	}

	go func() {
		zap.L().Info("Starting health server")
		if err := httpServer.Serve(listener); err != nil {
			log.Fatal(err)
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			err := h.report(ctx)
			if err != nil {
				zap.L().Error("Error reporting healthcheck", zap.Error(err))
			}
		}
	}
}

func (h *Healthcheck) report(ctx context.Context) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	childCtx, cancel := context.WithTimeout(ctx, healthcheckTimeout)
	defer cancel()

	// Update last run on report
	h.lastRun = time.Now()

	// Report sandbox count
	sbxCount := h.server.sandboxes.Count()
	h.sandboxCounter.Record(childCtx, int64(sbxCount))

	// Report health
	c, err := h.grpcHealth.Check(childCtx, &healthpb.HealthCheckRequest{
		// Empty string is the default service name
		Service: "",
	})
	zap.L().Info("grpc health", zap.Any("c", c), zap.Error(err))
	if err != nil {
		h.status = Unhealthy
		return err
	}

	switch c.GetStatus() {
	case healthpb.HealthCheckResponse_SERVING:
		h.status = Healthy
	default:
		h.status = Unhealthy
	}

	return nil
}

type HealthResponse struct {
	Status  Status `json:"status"`
	Version string `json:"version"`
}

func (h *Healthcheck) healthHandler(w http.ResponseWriter, r *http.Request) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	response := HealthResponse{
		Status:  h.status,
		Version: h.version,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	if err := json.NewEncoder(w).Encode(response); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
