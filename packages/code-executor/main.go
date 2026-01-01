package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/code-executor/internal/handlers"
	"github.com/e2b-dev/infra/packages/code-executor/internal/piston"
	"github.com/e2b-dev/infra/packages/code-executor/internal/worker"
)

const (
	defaultPort = 8080
)

func main() {
	var (
		port           int
		workers        int
		pistonURL      string
		debug          bool
	)

	// Get default piston URL from environment or use default
	defaultPistonURL := os.Getenv("PISTON_URL")
	if defaultPistonURL == "" {
		defaultPistonURL = "http://localhost:2000"
	}

	flag.IntVar(&port, "port", defaultPort, "Port for HTTP server")
	flag.IntVar(&workers, "workers", 10, "Number of workers for parallel execution")
	flag.StringVar(&pistonURL, "piston-url", defaultPistonURL, "Piston API URL")
	flag.BoolVar(&debug, "debug", false, "Enable debug mode")
	flag.Parse()

	// Initialize logger
	var logger *zap.Logger
	var err error
	if debug {
		logger, err = zap.NewDevelopment()
	} else {
		logger, err = zap.NewProduction()
	}
	if err != nil {
		log.Fatalf("Failed to initialize logger: %v", err)
	}
	defer logger.Sync()

	// Log configuration
	logger.Info("Configuration", zap.String("pistonURL", pistonURL), zap.Int("port", port), zap.Int("workers", workers))

	// Initialize Piston client
	pistonClient := piston.NewClient(pistonURL, logger)

	// Initialize worker pool
	workerPool := worker.NewPool(workers, logger)

	// Check if port is available, if not find a free port
	actualPort, err := checkPort(port, logger)
	if err != nil {
		logger.Fatal("Failed to find available port", zap.Error(err))
	}
	if actualPort != port {
		logger.Warn("Port is busy, using alternative port", zap.Int("requested", port), zap.Int("actual", actualPort))
	}

	// Initialize handlers
	handler := handlers.NewHandler(pistonClient, workerPool, logger)

	// Setup router
	router := setupRouter(handler, logger)

	// Create HTTP server
	srv := &http.Server{
		Addr:    fmt.Sprintf("0.0.0.0:%d", actualPort),
		Handler: router,
	}

	// Start server in goroutine
	go func() {
		logger.Info("Starting HTTP server", zap.Int("port", actualPort), zap.Int("workers", workers))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatal("Failed to start server", zap.Error(err))
		}
	}()

	// Wait for interrupt signal to gracefully shutdown the server
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Info("Shutting down server...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		logger.Fatal("Server forced to shutdown", zap.Error(err))
	}

	logger.Info("Server exited")
}

func setupRouter(handler *handlers.Handler, logger *zap.Logger) *gin.Engine {
	if !gin.IsDebugging() {
		gin.SetMode(gin.ReleaseMode)
	}

	r := gin.New()
	r.Use(gin.Recovery())

	// CORS configuration
	corsConfig := cors.DefaultConfig()
	corsConfig.AllowAllOrigins = true
	corsConfig.AllowHeaders = []string{"Origin", "Content-Length", "Content-Type"}
	r.Use(cors.New(corsConfig))

	// Health check
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	// API routes
	api := r.Group("/")
	{
		api.POST("/execute", handler.Execute)
		api.POST("/tests", handler.Tests)
	}

	return r
}

// checkPort checks if the port is available, returns the port or a free port
func checkPort(requestedPort int, logger *zap.Logger) (int, error) {
	// Try to listen on the requested port
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", requestedPort))
	if err != nil {
		// Port is busy, try to find a free port
		logger.Warn("Requested port is busy, searching for free port", zap.Int("port", requestedPort))
		listener, err = net.Listen("tcp", ":0")
		if err != nil {
			return 0, fmt.Errorf("failed to find free port: %w", err)
		}
	}
	defer listener.Close()

	addr := listener.Addr().(*net.TCPAddr)
	return addr.Port, nil
}

