package main

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
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
)

const (
	dnsServer       = "api.service.consul:5353"
	healthCheckPort = 3001
	port            = 3002
	sandboxPort     = 3003
	maxRetries      = 3
)

var commitSHA string

// Create a DNS client
var client = new(dns.Client)

func proxyHandler(logger *zap.SugaredLogger) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		logger.Debug(fmt.Sprintf("request for %s %s", r.Host, r.URL.Path))

		// Extract sandbox id from the sandboxID (<port>-<sandbox id>-<old client id>.e2b.dev)
		hostSplit := strings.Split(r.Host, "-")
		if len(hostSplit) < 2 {
			logger.Warn("invalid host", zap.String("host", r.Host))
			http.Error(w, "Invalid host", http.StatusBadRequest)

			return
		}

		sandboxID := hostSplit[1]
		msg := new(dns.Msg)

		// Set the question
		msg.SetQuestion(fmt.Sprintf("%s.", sandboxID), dns.TypeA)

		var node string
		var err error
		for i := range maxRetries {
			// Send the query to the server
			resp, _, dnsErr := client.Exchange(msg, dnsServer)

			// The api server wasn't found, maybe the API server is rolling and the DNS server is not updated yet
			if dnsErr != nil || len(resp.Answer) == 0 {
				err = dnsErr
				logger.Warn(fmt.Sprintf("host for sandbox %s not found: %s", sandboxID, err), zap.String("sandbox_id", sandboxID), zap.Error(err), zap.Int("retry", i+1))
				// Jitter
				time.Sleep(time.Duration(rand.Intn(10)) * time.Millisecond)

				continue
			}

			node = resp.Answer[0].(*dns.A).A.String()
			// The sandbox was not found, we want to return this information to the user
			if node == "127.0.0.1" {
				logger.Warn("Sandbox not found", zap.String("sandbox_id", sandboxID))
				w.WriteHeader(http.StatusBadGateway)
				w.Write([]byte("Sandbox not found"))

				return
			}

			break
		}

		// There's no answer, we can't proxy the request
		if err != nil {
			logger.Error("DNS resolving for failed", zap.String("sandbox_id", sandboxID), zap.Error(err))
			http.Error(w, "Host not found", http.StatusBadGateway)
			return
		}

		// We've resolved the node to proxy the request to
		logger.Debug("Proxying request", zap.String("sandbox_id", sandboxID), zap.String("node", node))
		targetUrl := &url.URL{
			Scheme: "http",
			Host:   fmt.Sprintf("%s:%d", node, sandboxPort),
		}

		// Proxy the request
		proxy := httputil.NewSingleHostReverseProxy(targetUrl)

		// Custom error handler for logging proxy errors
		proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
			logger.Error("Reverse proxy error", zap.Error(err), zap.String("sandbox_id", sandboxID))
			http.Error(w, "Proxy error", http.StatusBadGateway)
		}

		// Modify response for logging or additional processing
		proxy.ModifyResponse = func(resp *http.Response) error {
			if resp.StatusCode >= 500 {
				logger.Error("Backend responded with error", zap.Int("status_code", resp.StatusCode), zap.String("sandbox_id", sandboxID))
			} else {
				logger.Info("Backend responded", zap.Int("status_code", resp.StatusCode), zap.String("sandbox_id", sandboxID), zap.String("node", node), zap.String("path", r.URL.Path))
			}

			return nil
		}

		// Set the transport options (with values similar to our old the nginx configuration)
		proxy.Transport = &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			MaxIdleConns:          1024,              // Matches worker_connections
			MaxIdleConnsPerHost:   8192,              // Matches keepalive_requests
			IdleConnTimeout:       620 * time.Second, // Matches keepalive_timeout
			TLSHandshakeTimeout:   10 * time.Second,  // Similar to client_header_timeout
			ResponseHeaderTimeout: 24 * time.Hour,    // Matches proxy_read_timeout
			DisableKeepAlives:     false,             // Allow keep-alives
		}

		proxy.ServeHTTP(w, r)
	}
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

	logger.Info("Starting client proxy", zap.String("commit", commitSHA))

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

	// Proxy request to the correct node
	server := &http.Server{Addr: fmt.Sprintf(":%d", port)}
	server.Handler = http.HandlerFunc(proxyHandler(logger))

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
		logger.Info("shutting down http service", zap.Int("port", healthCheckPort))
		if err := healthServer.Shutdown(ctx); err != nil {
			exitCode.Add(1)
			logger.Error("http service shutdown error", zap.Int("port", healthCheckPort), zap.Error(err))
		}

		logger.Info("waiting 15 seconds before shutting down http service")
		time.Sleep(15 * time.Second)

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
