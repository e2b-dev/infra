package server

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"sync"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
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
}

func NewHealthcheck(server *server, grpc *grpc.Server, grpcHealth *health.Server, version string) (*Healthcheck, error) {
	return &Healthcheck{
		version:    version,
		server:     server,
		grpc:       grpc,
		grpcHealth: grpcHealth,

		lastRun: time.Now(),
		status:  Unhealthy,
		mu:      sync.RWMutex{},
	}, nil
}

func (h *Healthcheck) Start(ctx context.Context, listener net.Listener) error {
	ticker := time.NewTicker(healthcheckFrequency)
	defer ticker.Stop()

	// Start /health HTTP server
	routeMux := http.NewServeMux()
	routeMux.HandleFunc("/health", h.healthHandler)
	httpServer := &http.Server{
		Handler: routeMux,
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	sig := make(chan struct{})

	go func() {
		defer cancel()
		defer close(sig)
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
	}()

	shutdownErr := make(chan error)
	go func() {
		defer close(shutdownErr)
		<-ctx.Done()

		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()

		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			select {
			case <-shutdownCtx.Done():
			case shutdownErr <- err:
			}
		}
	}()

	zap.L().Info("Starting health server")
	if err := httpServer.Serve(listener); !errors.Is(err, http.ErrServerClosed) {
		return err
	}

	<-sig
	return <-shutdownErr
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
func (h *Healthcheck) getGRPCHealth(ctx context.Context) (Status, error) {
	c, err := h.grpcHealth.Check(ctx, &healthpb.HealthCheckRequest{
		// Empty string is the default service name
		Service: "",
	})
	if err != nil {
		return Unhealthy, err
	}

	switch c.GetStatus() {
	case healthpb.HealthCheckResponse_SERVING:
		return Healthy, nil
	default:
		return Unhealthy, nil
	}
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
	if h.status == Unhealthy {
		w.WriteHeader(http.StatusServiceUnavailable)
	} else {
		w.WriteHeader(http.StatusOK)
	}

	if err := json.NewEncoder(w).Encode(response); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
