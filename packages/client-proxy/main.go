package main

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"text/template"
	"time"

	"github.com/google/uuid"
	"github.com/miekg/dns"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/launchdarkly/go-sdk-common/v3/ldcontext"

	"github.com/e2b-dev/infra/packages/shared/pkg/env"
	featureflags "github.com/e2b-dev/infra/packages/shared/pkg/feature-flags"
	e2bLogger "github.com/e2b-dev/infra/packages/shared/pkg/logger"

	"github.com/e2b-dev/infra/packages/shared/pkg/meters"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const (
	ServiceName           = "client-proxy"
	dnsServer             = "api.service.consul:5353"
	healthCheckPort       = 3001
	port                  = 3002
	sandboxPort           = 3003 // legacy session proxy port
	orchestratorProxyPort = 5007 // orchestrator proxy port
	maxRetries            = 3
)

var (
	//go:embed html-templates/sandbox_not_found_502.html
	sandboxNotFound502Html         string
	sandboxNotFound502HtmlTemplate = template.Must(template.New("template").Parse(sandboxNotFound502Html))
	browserUserAgentRegex          = regexp.MustCompile(`(?i)mozilla|chrome|safari|firefox|edge|opera|msie`)

	commitSHA string

	// Create a DNS client
	client = new(dns.Client)
)

type htmlTemplateData struct {
	SandboxId   string
	SandboxHost string
}

type jsonTemplateData struct {
	Message   string `json:"message"`
	SandboxId string `json:"sandboxId"`
	Code      int    `json:"code"`
}

func proxyHandler(transport *http.Transport, featureFlags *featureflags.Client) func(w http.ResponseWriter, r *http.Request) {
	activeConnections, err := meters.GetUpDownCounter(meters.ActiveConnectionsCounterMeterName)
	if err != nil {
		zap.L().Error("failed to create active connections counter", zap.Error(err))
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if activeConnections != nil {
			activeConnections.Add(r.Context(), 1)
			defer func() {
				activeConnections.Add(r.Context(), -1)
			}()
		}

		// Extract sandbox id from the sandboxID (<port>-<sandbox id>-<old client id>.e2b.app)
		hostSplit := strings.Split(r.Host, "-")
		if len(hostSplit) < 2 {
			zap.L().Warn("invalid host", zap.String("host", r.Host))
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
				zap.L().Warn(fmt.Sprintf("host for sandbox %s not found: %s", sandboxID, err), zap.String("sandbox_id", sandboxID), zap.Error(err), zap.Int("retry", i+1))
				// Jitter
				time.Sleep(time.Duration(rand.Intn(10)) * time.Millisecond)

				continue
			}

			node = resp.Answer[0].(*dns.A).A.String()
			// The sandbox was not found, we want to return this information to the user
			if node == "127.0.0.1" {
				zap.L().Warn("Sandbox not found", zap.String("sandbox_id", sandboxID))

				if isBrowser(r.UserAgent()) {
					res, resErr := buildHtmlNotFoundError(sandboxID, r.Host)
					if resErr != nil {
						zap.L().Error("Failed to build sandbox not found HTML error response", zap.Error(resErr))
						w.WriteHeader(http.StatusInternalServerError)
						return
					}
					w.WriteHeader(http.StatusBadGateway)
					w.Header().Add("Content-Type", "text/html")
					w.Write(res)
					return
				} else {
					res, resErr := buildJsonNotFoundError(sandboxID)
					if resErr != nil {
						zap.L().Error("Failed to build JSON error response", zap.Error(resErr))
						w.WriteHeader(http.StatusInternalServerError)
						return
					}
					w.WriteHeader(http.StatusBadGateway)
					w.Header().Add("Content-Type", "application/json")
					w.Write(res)
					return
				}
			}

			break
		}

		// There's no answer, we can't proxy the request
		if err != nil {
			zap.L().Error("DNS resolving for failed", zap.String("sandbox_id", sandboxID), zap.Error(err))
			http.Error(w, "Host not found", http.StatusBadGateway)
			return
		}

		// We've resolved the node to proxy the request to
		zap.L().Debug("Proxying request", zap.String("sandbox_id", sandboxID), zap.String("node", node))

		flagCtx := ldcontext.NewBuilder("client-proxy-context").SetString("sandbox_id", sandboxID).Build()
		flag, flagErr := featureFlags.Ld.StringVariation(featureflags.ClientProxyTrafficTargetFeatureFlag, flagCtx, featureflags.ClientProxyTrafficToNginx)
		if flagErr != nil {
			zap.L().Error("soft failing during feature flag receive", zap.Error(flagErr))
		}

		var targetPort int
		if flag == featureflags.ClientProxyTrafficToOrchestrator {
			// Proxy traffic to orchestrator
			zap.L().Info("Proxying traffic to orchestrator", zap.String("sandbox_id", sandboxID), zap.String("node", node))
			targetPort = orchestratorProxyPort
		} else {
			// Proxy traffic to nginx proxy
			zap.L().Info("Proxying traffic to nginx", zap.String("sandbox_id", sandboxID), zap.String("node", node))
			targetPort = sandboxPort
		}

		targetUrl := &url.URL{
			Scheme: "http",
			Host:   fmt.Sprintf("%s:%d", node, targetPort),
		}

		// Proxy the request
		proxy := httputil.NewSingleHostReverseProxy(targetUrl)

		// Custom error handler for logging proxy errors
		proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
			zap.L().Error("Reverse proxy error", zap.Error(err), zap.String("sandbox_id", sandboxID))
			http.Error(w, "Proxy error", http.StatusBadGateway)
		}

		// Modify response for logging or additional processing
		proxy.ModifyResponse = func(resp *http.Response) error {
			if resp.StatusCode >= 500 {
				zap.L().Error("Backend responded with error", zap.Int("status_code", resp.StatusCode), zap.String("sandbox_id", sandboxID))
			} else {
				zap.L().Info("Backend responded", zap.Int("status_code", resp.StatusCode), zap.String("sandbox_id", sandboxID), zap.String("node", node), zap.String("path", r.URL.Path))
			}

			return nil
		}

		// Set the transport
		proxy.Transport = transport
		proxy.ServeHTTP(w, r)
	}
}

func run() int {
	exitCode := atomic.Int32{}
	wg := sync.WaitGroup{}

	ctx := context.Background()
	signalCtx, sigCancel := signal.NotifyContext(ctx, syscall.SIGTERM, syscall.SIGINT)
	defer sigCancel()

	instanceID := uuid.New().String()
	stopOtlp := telemetry.InitOTLPExporter(ctx, ServiceName, commitSHA, instanceID)
	defer func() {
		err := stopOtlp(ctx)
		if err != nil {
			log.Printf("telemetry shutdown:%v\n", err)
		}
	}()

	logger := zap.Must(e2bLogger.NewLogger(ctx, e2bLogger.LoggerConfig{
		ServiceName: ServiceName,
		IsInternal:  true,
		IsDebug:     env.IsDebug(),
		Cores:       []zapcore.Core{e2bLogger.GetOTELCore(ServiceName)},
	}))
	defer func() {
		err := logger.Sync()
		if err != nil {
			log.Printf("logger sync error: %v\n", err)
		}
	}()
	zap.ReplaceGlobals(logger)

	featureFlags, err := featureflags.NewClient(5 * time.Second)
	if err != nil {
		logger.Error("failed to create feature flags client", zap.Error(err))
		return 1
	}
	defer featureFlags.Close()

	logger.Info("Starting client proxy", zap.String("commit", commitSHA), zap.String("instance_id", instanceID))

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

	// similar values to our old the nginx configuration
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		MaxIdleConns:          1024,              // Matches worker_connections
		MaxIdleConnsPerHost:   8192,              // Matches keepalive_requests
		IdleConnTimeout:       620 * time.Second, // Matches keepalive_timeout
		TLSHandshakeTimeout:   10 * time.Second,  // Similar to client_header_timeout
		ResponseHeaderTimeout: 24 * time.Hour,    // Matches proxy_read_timeout
		DisableKeepAlives:     true,              // Disable keep-alive, not supported
	}

	server.Handler = http.HandlerFunc(proxyHandler(transport, featureFlags))

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

	return int(exitCode.Load())
}

func buildHtmlNotFoundError(sandboxId string, host string) ([]byte, error) {
	htmlResponse := new(bytes.Buffer)
	htmlVars := htmlTemplateData{SandboxId: sandboxId, SandboxHost: host}

	err := sandboxNotFound502HtmlTemplate.Execute(htmlResponse, htmlVars)
	if err != nil {
		return nil, err
	}

	return htmlResponse.Bytes(), nil
}

func buildJsonNotFoundError(sandboxId string) ([]byte, error) {
	response := jsonTemplateData{
		Message:   "Sandbox not found",
		SandboxId: sandboxId,
	}

	responseBytes, err := json.Marshal(response)
	if err != nil {
		return nil, err
	}

	return responseBytes, nil
}

func isBrowser(userAgent string) bool {
	return browserUserAgentRegex.MatchString(userAgent)
}

func main() {
	// Exit, with appropriate code.
	os.Exit(run())
}
