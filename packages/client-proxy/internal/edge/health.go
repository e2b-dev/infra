package edge

import (
	"fmt"
	"go.uber.org/zap"
	"net/http"
)

type HealthServer struct {
	server *http.Server
	logger *zap.Logger
	port   int

	serviceHealth *bool
	trafficHealth *bool
}

func NewHealthServer(port int, logger *zap.Logger) *HealthServer {
	mux := http.NewServeMux()

	serviceHealth := true
	trafficHealth := true

	mux.HandleFunc(
		"/health",
		func(w http.ResponseWriter, r *http.Request) {
			if !serviceHealth {
				w.WriteHeader(http.StatusBadRequest)
				w.Write([]byte("unhealthy"))
				return
			}

			w.WriteHeader(http.StatusOK)
			w.Write([]byte("healthy"))
		},
	)

	mux.HandleFunc(
		"/health/traffic",
		func(w http.ResponseWriter, r *http.Request) {
			if !trafficHealth {
				w.WriteHeader(http.StatusBadRequest)
				w.Write([]byte("unhealthy"))
				return
			}

			w.WriteHeader(http.StatusOK)
			w.Write([]byte("healthy"))
		},
	)

	return &HealthServer{
		logger: logger,
		port:   port,
		server: &http.Server{
			Addr:    fmt.Sprintf(":%d", port),
			Handler: mux,
		},

		serviceHealth: &serviceHealth,
		trafficHealth: &trafficHealth,
	}
}

func (h *HealthServer) Start() error {
	h.logger.Info("Health server listening", zap.Int("port", h.port))

	err := h.server.ListenAndServe()
	if err != nil {
		h.logger.Error("Health server failed", zap.Error(err))
		return err
	}

	return nil
}

func (h *HealthServer) SetTrafficHealth(s bool) {
	h.trafficHealth = &s
}

func (h *HealthServer) SetServiceHealth(s bool) {
	h.serviceHealth = &s
}
