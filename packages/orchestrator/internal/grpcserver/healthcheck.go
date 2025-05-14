package grpcserver

import (
	"context"
	"encoding/json"
	"log"
	"net"
	"net/http"
	"sync"
	"time"

	"go.uber.org/zap"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	e2bHealth "github.com/e2b-dev/infra/packages/shared/pkg/health"
)

const healthcheckFrequency = 5 * time.Second
const healthcheckTimeout = 30 * time.Second

type Healthcheck struct {
	version string
	grpc    *GRPCServer

	// TODO: Replace with status from SQL Lite
	status  e2bHealth.Status
	lastRun time.Time
	mu      sync.RWMutex
}

func NewHealthcheck(grpc *GRPCServer, version string) (*Healthcheck, error) {
	return &Healthcheck{
		version: version,
		grpc:    grpc,

		lastRun: time.Now(),
		status:  e2bHealth.Unhealthy,
		mu:      sync.RWMutex{},
	}, nil
}

func (h *Healthcheck) Start(ctx context.Context, listener net.Listener) {
	ticker := time.NewTicker(healthcheckFrequency)
	defer ticker.Stop()

	// Start /health HTTP server
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

// report updates the health status.
// This function is run in a goroutine every healthcheckFrequency for the reason of having
// longer running tasks that might be too slow or resource intensive to be run
// in the healthcheck http handler directly.
func (h *Healthcheck) report(ctx context.Context) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	childCtx, cancel := context.WithTimeout(ctx, healthcheckTimeout)
	defer cancel()

	// Update last run on report
	h.lastRun = time.Now()

	// Report health of the gRPC
	var err error
	h.status, err = h.getGRPCHealth(childCtx)
	if err != nil {
		return err
	}

	return nil
}

// getGRPCHealth returns the health status of the grpc.Server by calling the health service check.
func (h *Healthcheck) getGRPCHealth(ctx context.Context) (e2bHealth.Status, error) {
	c, err := h.grpc.HealthServer().Check(ctx, &healthpb.HealthCheckRequest{
		// Empty string is the default service name
		Service: "",
	})
	if err != nil {
		return e2bHealth.Unhealthy, err
	}

	switch c.GetStatus() {
	case healthpb.HealthCheckResponse_SERVING:
		return e2bHealth.Healthy, nil
	default:
		return e2bHealth.Unhealthy, nil
	}
}

func (h *Healthcheck) healthHandler(w http.ResponseWriter, r *http.Request) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	response := e2bHealth.Response{Status: h.status}

	w.Header().Set("Content-Type", "application/json")
	if h.status == e2bHealth.Unhealthy {
		w.WriteHeader(http.StatusServiceUnavailable)
	} else {
		w.WriteHeader(http.StatusOK)
	}

	if err := json.NewEncoder(w).Encode(response); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
