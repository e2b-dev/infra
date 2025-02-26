package main

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/miekg/dns"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/env"
	"github.com/e2b-dev/infra/packages/shared/pkg/logging"
	"github.com/e2b-dev/infra/packages/shared/pkg/meters"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const ServiceName = "client_proxy"

// Build information
var commit = "dev"

const (
	dnsServer       = "api.service.consul:5353"
	healthCheckPort = 3001
	port            = 3002
	sandboxPort     = 3003
	maxRetries      = 3
)

// Create a DNS client
var client = new(dns.Client)


// MeteredTransport wraps the standard http.Transport and adds connection metering
type MeteredTransport struct {
	*http.Transport
	activeConnections meters.UpDownCounter
	logger            *zap.SugaredLogger
}

// NewMeteredTransport creates a new http Transport with connection metering
func NewMeteredTransport(logger *zap.SugaredLogger) (*MeteredTransport, error) {
	activeConnections, err := meters.GetUpDownCounter(meters.ActiveConnectionsCounterMeterName)
	if err != nil {
		logger.Error("failed to create active connections counter", zap.Error(err))
		// Still return a transport, it just won't meter connections
		activeConnections = nil
	}

	transport := &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     90 * time.Second,
	}

	mt := &MeteredTransport{
		Transport:         transport,
		activeConnections: activeConnections,
		logger:            logger,
	}

	// Override the DialContext function to meter connections
	mt.Transport.DialContext = mt.meteredDialContext
	
	return mt, nil
}

// meteredDialContext wraps the default DialContext to track active connections
func (mt *MeteredTransport) meteredDialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	// Use the default dialer to create the connection
	dialer := &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
	}
	
	conn, err := dialer.DialContext(ctx, network, addr)
	if err != nil {
		return nil, err
	}
	
	// Increment the connection counter
	if mt.activeConnections != nil {
		mt.activeConnections.Add(ctx, 1)
		mt.logger.Debug("opened new connection", zap.String("addr", addr))
	}
	
	// Return a wrapped connection that decrements the counter when closed
	return &meteredConn{
		Conn:              conn,
		activeConnections: mt.activeConnections,
		ctx:               ctx,
		logger:            mt.logger,
		addr:              addr,
	}, nil
}

// meteredConn wraps a net.Conn to track when it's closed
type meteredConn struct {
	net.Conn
	activeConnections meters.UpDownCounter
	ctx               context.Context
	logger            *zap.SugaredLogger
	addr              string
	closed            bool
}

// Close overrides the default Close method to decrement the connection counter
func (mc *meteredConn) Close() error {
	if !mc.closed {
		mc.closed = true
		if mc.activeConnections != nil {
			mc.activeConnections.Add(mc.ctx, -1)
			mc.logger.Debug("closed connection", zap.String("addr", mc.addr))
		}
	}
	return mc.Conn.Close()
}

// resolveSandboxNode resolves the sandbox ID to a node IP address
func resolveSandboxNode(logger *zap.SugaredLogger, sandboxID string) (string, error) {
	msg := new(dns.Msg)
	// Set the question
	msg.SetQuestion(fmt.Sprintf("%s.", sandboxID), dns.TypeA)

	var node string
	var err error
	for i := 0; i < maxRetries; i++ {
		// Send the query to the server
		resp, _, dnsErr := client.Exchange(msg, dnsServer)

		// The api server wasn't found, maybe the API server is rolling and the DNS server is not updated yet
		if dnsErr != nil || resp == nil || len(resp.Answer) == 0 {
			err = dnsErr
			if err == nil {
				err = errors.New("empty DNS response")
			}
			logger.Warn(fmt.Sprintf("host for sandbox %s not found: %s", sandboxID, err), 
				zap.String("sandbox_id", sandboxID), 
				zap.Error(err), 
				zap.Int("retry", i+1))
			// Jitter
			time.Sleep(time.Duration(rand.Intn(10)) * time.Millisecond)
			continue
		}

		aRecord, ok := resp.Answer[0].(*dns.A)
		if !ok {
			err = fmt.Errorf("unexpected DNS response type: %T", resp.Answer[0])
			logger.Warn("Unexpected DNS response type", 
				zap.String("sandbox_id", sandboxID), 
				zap.Error(err), 
				zap.Int("retry", i+1))
			continue
		}

		node = aRecord.A.String()
		// The sandbox was not found, we want to return this information to the user
		if node == "127.0.0.1" {
			return "", fmt.Errorf("sandbox not found")
		}

		return node, nil
	}

	// There's no answer, we can't proxy the request
	if err == nil {
		err = errors.New("failed to resolve sandbox after max retries")
	}
	
	return "", err
}

// createProxyWithMeteredTransport creates a handler that uses a metered transport
func createProxyWithMeteredTransport(logger *zap.SugaredLogger) http.Handler {
	meteredTransport, err := NewMeteredTransport(logger)
	if err != nil {
		logger.Error("failed to create metered transport", zap.Error(err))
		// Fall back to standard transport
		meteredTransport = &MeteredTransport{Transport: &http.Transport{}}
	}
	
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logger.Debug(fmt.Sprintf("request for %s %s", r.Host, r.URL.Path))

		// Extract sandbox id from the sandboxID (<port>-<sandbox id>-<old client id>.e2b.dev)
		hostSplit := strings.Split(r.Host, "-")
		if len(hostSplit) < 2 {
			logger.Warn("invalid host", zap.String("host", r.Host))
			http.Error(w, "Invalid host", http.StatusBadRequest)
			return
		}

		sandboxID := hostSplit[1]
		
		// Resolve the sandbox node
		node, err := resolveSandboxNode(logger, sandboxID)
		if err != nil {
			if err.Error() == "sandbox not found" {
				logger.Warn("Sandbox not found", zap.String("sandbox_id", sandboxID))
				w.WriteHeader(http.StatusBadGateway)
				w.Write([]byte("Sandbox not found"))
			} else {
				logger.Error("DNS resolving failed", zap.String("sandbox_id", sandboxID), zap.Error(err))
				http.Error(w, "Host not found", http.StatusBadGateway)
			}
			return
		}

		// We've resolved the node to proxy the request to
		logger.Debug("proxying request", zap.String("sandbox_id", sandboxID), zap.String("node", node))
		targetUrl := &url.URL{
			Scheme: "http",
			Host:   fmt.Sprintf("%s:%d", node, sandboxPort),
		}

		// Create a reverse proxy with the metered transport
		proxy := httputil.NewSingleHostReverseProxy(targetUrl)
		
		// Replace the default transport with our metered one
		proxy.Transport = meteredTransport
		
		proxy.ServeHTTP(w, r)
	})
}

func main() {
	exitCode := atomic.Int32{}
	wg := sync.WaitGroup{}

	ctx := context.Background()

	signalCtx, sigCancel := signal.NotifyContext(ctx, syscall.SIGTERM, syscall.SIGINT)
	defer sigCancel()

	logger, err := logging.New(env.IsLocal())
	if err != nil {
		panic(fmt.Errorf("error creating logger: %v", err))
	}

	logger.Info("starting client proxy", zap.String("commit", commit))

	healthServer := &http.Server{Addr: fmt.Sprintf(":%d", healthCheckPort)}
	healthServer.Handler = http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusOK)
	})

	wg.Add(1)
	go func() {
		// Health check
		defer wg.Done()

		logger.Info("starting health check server", zap.Int("port", healthCheckPort))
		err := healthServer.ListenAndServe()
		switch {
		case errors.Is(err, http.ErrServerClosed):
			logger.Info("http service shutdown successfully", zap.Int("port", healthCheckPort))
		case err != nil:
			exitCode.Add(1)
			logger.Error("http service encountered error", zap.Int("port", healthCheckPort), zap.Error(err))
		default:
			// this probably shouldn't happen...
			logger.Error("http service exited without error", zap.Int("port", healthCheckPort))
		}
	}()

	cleanupTelemetry := telemetry.InitOTLPExporter(context.TODO(), ServiceName, "no")
	defer cleanupTelemetry(ctx)

	// Create the handler with metered transport
	handler := createProxyWithMeteredTransport(logger)

	// Proxy request to the correct node
	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: handler,
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		// make sure to cancel the parent context before this
		// goroutine returns, so that in the case of a panic
		// or error here, the other thread won't block until
		// signaled.
		defer sigCancel()

		logger.Info("http service starting", zap.Int("port", port))
		err := server.ListenAndServe()
		// Add different handling for the error
		switch {
		case errors.Is(err, http.ErrServerClosed):
			logger.Info("http service shutdown successfully", zap.Int("port", port))
		case err != nil:
			exitCode.Add(1)
			logger.Error("http service encountered error", zap.Int("port", port), zap.Error(err))
		default:
			// this probably shouldn't happen...
			logger.Error("http service exited without error", zap.Int("port", port))
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		<-signalCtx.Done()
		logger.Info("shutting down http service", zap.Int("port", port))
		if err := healthServer.Shutdown(ctx); err != nil {
			exitCode.Add(1)
			logger.Error("http service shutdown error", zap.Int("port", healthCheckPort), zap.Error(err))
		}

		logger.Info("waiting 15 seconds before shutting down http service")
		time.Sleep(15 * time.Second)

		logger.Info("shutting down telemetry")

		err := cleanupTelemetry(ctx)
		if err != nil {
			logger.Error("error shutting down telemetry", zap.Error(err))
		}

		logger.Info("shutting down http service", zap.Int("port", port))

		if err := server.Shutdown(ctx); err != nil {
			exitCode.Add(1)
			logger.Error("http service shutdown error", zap.Int("port", port), zap.Error(err))
		}
	}()

	wg.Wait()

	// Exit, with appropriate code.
	os.Exit(int(exitCode.Load()))
}