// Command kubernetes-orchestrator exposes the E2B orchestrator gRPC and proxy
// contracts while running sandboxes as Kata-backed Kubernetes Pods.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/kubernetesserver"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	orchestratorinfo "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator-info"
)

const defaultVersion = "0.1.0-kubernetes"

var commitSHA string

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	version := os.Getenv("SERVICE_VERSION")
	if version == "" {
		version = defaultVersion
	}
	commit := commitSHA
	if commit == "" {
		commit = "unknown"
	}
	config, err := kubernetesserver.ConfigFromEnv(version, commit)
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}

	restConfig, err := kubernetesConfig()
	if err != nil {
		return err
	}
	client, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return fmt.Errorf("create Kubernetes client: %w", err)
	}
	sandboxServer, err := kubernetesserver.New(client, config)
	if err != nil {
		return fmt.Errorf("create sandbox server: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	var listenConfig net.ListenConfig
	grpcListener, err := listenConfig.Listen(ctx, "tcp", fmt.Sprintf(":%d", config.GRPCPort))
	if err != nil {
		return fmt.Errorf("listen on gRPC port: %w", err)
	}
	grpcServer := grpc.NewServer()
	orchestrator.RegisterSandboxServiceServer(grpcServer, sandboxServer)
	orchestrator.RegisterChunkServiceServer(grpcServer, &orchestrator.UnimplementedChunkServiceServer{})
	orchestrator.RegisterVolumeServiceServer(grpcServer, &orchestrator.UnimplementedVolumeServiceServer{})
	orchestratorinfo.RegisterInfoServiceServer(grpcServer, kubernetesserver.NewInfo(client, config))
	healthServer := health.NewServer()
	healthServer.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
	healthServer.SetServingStatus("SandboxService", healthpb.HealthCheckResponse_SERVING)
	healthpb.RegisterHealthServer(grpcServer, healthServer)

	proxyServer, err := kubernetesserver.NewProxy(ctx, client, config)
	if err != nil {
		return fmt.Errorf("create sandbox proxy: %w", err)
	}
	healthMux := http.NewServeMux()
	healthMux.HandleFunc("/health", func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte("ok"))
	})
	healthMux.HandleFunc("/ready", func(writer http.ResponseWriter, request *http.Request) {
		readyCtx, cancel := context.WithTimeout(request.Context(), 2*time.Second)
		defer cancel()
		if _, err := client.CoreV1().Pods(config.Namespace).List(readyCtx, metav1.ListOptions{Limit: 1}); err != nil {
			http.Error(writer, "Kubernetes API unavailable", http.StatusServiceUnavailable)
			return
		}
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte("ready"))
	})
	httpServer := &http.Server{
		Addr:              fmt.Sprintf(":%d", config.HealthPort),
		Handler:           healthMux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	log.Printf("kubernetes orchestrator listening grpc=:%d proxy=:%d health=:%d namespace=%s runtimes=%v clientID=%s",
		config.GRPCPort, config.ProxyPort, config.HealthPort, config.Namespace, config.AllowedRuntimeClasses, sandboxServer.ClientID())

	errCh := make(chan error, 3)
	go func() { errCh <- grpcServer.Serve(grpcListener) }()
	go func() { errCh <- proxyServer.ListenAndServe(ctx) }()
	go func() { errCh <- httpServer.ListenAndServe() }()

	var unexpectedErr error
	select {
	case <-ctx.Done():
	case serveErr := <-errCh:
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) && !errors.Is(serveErr, grpc.ErrServerStopped) && !errors.Is(serveErr, context.Canceled) {
			log.Printf("server stopped unexpectedly: %v", serveErr)
			unexpectedErr = serveErr
		}
	}
	stop()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	healthServer.Shutdown()
	if err := proxyServer.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Printf("proxy shutdown: %v", err)
	}
	if err := httpServer.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Printf("health server shutdown: %v", err)
	}
	grpcStopped := make(chan struct{})
	go func() {
		grpcServer.GracefulStop()
		close(grpcStopped)
	}()
	select {
	case <-grpcStopped:
	case <-shutdownCtx.Done():
		grpcServer.Stop()
	}
	if unexpectedErr != nil {
		return fmt.Errorf("server stopped unexpectedly: %w", unexpectedErr)
	}
	return nil
}

func kubernetesConfig() (*rest.Config, error) {
	if kubeconfig := os.Getenv("KUBECONFIG"); kubeconfig != "" {
		config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil {
			return nil, fmt.Errorf("load KUBECONFIG: %w", err)
		}
		return config, nil
	}
	config, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("load in-cluster Kubernetes configuration: %w", err)
	}
	return config, nil
}
