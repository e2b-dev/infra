package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/e2b-dev/infra/packages/shared/pkg/env"
	"github.com/e2b-dev/infra/packages/shared/pkg/logging"
	"github.com/miekg/dns"
	"go.uber.org/zap"
)

const dnsServer = "api.service.consul:5353"
const healthCheckPort = 3001
const port = 3002
const sandboxPort = 3003

// Create a DNS client
var client = new(dns.Client)

func proxy(logger *zap.SugaredLogger) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		logger.Debug("Request for %s %s", r.Host, r.URL.Path)

		// Extract sandbox id from the sandboxID (<port>-<sandbox id>-<old client id>.e2b.dev)
		sandboxID := strings.Split(r.Host, "-")[1]
		msg := new(dns.Msg)

		// Set the question
		msg.SetQuestion(fmt.Sprintf("%s.", sandboxID), dns.TypeA)

		var resp *dns.Msg
		var err error
		for i := range 3 {
			// Send the query to the server
			resp, _, err = client.Exchange(msg, dnsServer)

			// The api server wasn't found, maybe the API server is rolling and the DNS server is not updated yet
			if err != nil || len(resp.Answer) == 0 {
				logger.Warnf("[%d] Host for sandbox %s not found: %s", i, sandboxID, err)

				// Jitter
				time.Sleep(time.Duration(rand.Intn(10)) * time.Millisecond)

				continue
			}

			// The sandbox was not found, we want to return this information to the user
			if resp.Answer[0].String() == "localhost" {
				w.WriteHeader(http.StatusBadGateway)
				w.Write([]byte("Sandbox not found"))

				return
			}

			break
		}

		// There's no answer, we can't proxy the request
		if err != nil || len(resp.Answer) == 0 {
			logger.Errorf("DNS resolving for %s failed: %s", sandboxID, err)
			http.Error(w, "Host not found", http.StatusBadGateway)
			return
		}

		// We've resolved the node to proxy the request to
		logger.Debugf("Proxying request for %s to %s", sandboxID, resp.Answer[0].String())
		targetUrl := &url.URL{
			Scheme: "http",
			Host:   fmt.Sprintf("%s:%d", resp.Answer[0].(*dns.A).A.String(), sandboxPort),
		}

		// Proxy the request
		httputil.NewSingleHostReverseProxy(targetUrl).ServeHTTP(w, r)
	}
}
func main() {
	exitCode := atomic.Int32{}

	ctx := context.Background()
	signalCtx, sigCancel := signal.NotifyContext(ctx, syscall.SIGTERM, syscall.SIGINT)
	defer sigCancel()

	logger, err := logging.New(env.IsLocal())
	if err != nil {
		log.Fatalf("error creating logger: %v", err)
	}

	healthServer := http.Server{Addr: fmt.Sprintf(":%d", healthCheckPort)}
	go func() {
		// Health check
		healthServer.Handler = http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			logger.Debug("Health check")
			writer.WriteHeader(http.StatusOK)
		})

		err := healthServer.ListenAndServe()
		if err != nil {
			// Add different handling for the error
			logger.Infof("server error: %v", err)
		}
	}()

	// Proxy request to the correct node
	server := http.Server{Addr: fmt.Sprintf(":%d", port)}
	server.Handler = http.HandlerFunc(proxy(logger))

	go func() {
		<-signalCtx.Done()
		logger.Infof("shutting down http service (%d)", healthCheckPort)
		if err := healthServer.Shutdown(ctx); err != nil {
			exitCode.Add(1)
			logger.Errorf("http service (%d) shutdown error: %v", healthCheckPort, err)
		}

		logger.Infof("shutting down http service (%d)", port)

		if err := server.Shutdown(ctx); err != nil {
			exitCode.Add(1)
			logger.Errorf("http service (%d) shutdown error: %v", port, err)
		}
	}()

	err = server.ListenAndServe()
	// Add different handling for the error
	switch {
	case errors.Is(err, http.ErrServerClosed):
		log.Printf("http service (%d) shutdown successfully", port)
	case err != nil:
		exitCode.Add(1)
		log.Printf("http service (%d) encountered error: %v", port, err)
	default:
		// this probably shouldn't happen...
		log.Printf("http service (%d) exited without error", port)
	}
}
