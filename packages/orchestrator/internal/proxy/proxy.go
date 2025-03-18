package proxy

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"go.uber.org/zap"
	"html/template"
	"net/http"
	"net/http/httputil"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/e2b-dev/infra/packages/shared/pkg/meters"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
)

//go:embed proxy_browser_502.html
var proxyBrowser502PageHtml string

var browserRegex = regexp.MustCompile(`(?i)mozilla|chrome|safari|firefox|edge|opera|msie`)
var browserTemplate = template.Must(template.New("template").Parse(proxyBrowser502PageHtml))

type htmlTemplateData struct {
	SandboxId   string
	SandboxHost string
	SandboxPort string
}

type jsonTemplateData struct {
	Error     string `json:"error"`
	SandboxId string `json:"sandbox_id"`
	Port      uint64 `json:"port"`
}

type SandboxProxy struct {
	sandboxes *smap.Map[string]
	server    *http.Server
}

func New(port uint) *SandboxProxy {
	server := &http.Server{Addr: fmt.Sprintf(":%d", port)}

	return &SandboxProxy{
		server:    server,
		sandboxes: smap.New[string](),
	}
}

func (p *SandboxProxy) AddSandbox(sandboxID, ip string) {
	p.sandboxes.Insert(sandboxID, ip)
}

func (p *SandboxProxy) RemoveSandbox(sandboxID string, ip string) {
	p.sandboxes.RemoveCb(sandboxID, func(k string, v string, ok bool) bool { return ok && v == ip })
}

func (p *SandboxProxy) Start() error {
	// similar values to our old the nginx configuration
	serverTransport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		MaxIdleConns:          1024,              // Matches worker_connections
		MaxIdleConnsPerHost:   8192,              // Matches keepalive_requests
		IdleConnTimeout:       620 * time.Second, // Matches keepalive_timeout
		TLSHandshakeTimeout:   10 * time.Second,  // Similar to client_header_timeout
		ResponseHeaderTimeout: 24 * time.Hour,    // Matches proxy_read_timeout
		DisableKeepAlives:     true,              // Disable keep-alives, envd doesn't support idle connections
	}

	p.server.Handler = http.HandlerFunc(p.proxyHandler(serverTransport))
	return p.server.ListenAndServe()
}

func (p *SandboxProxy) Shutdown(ctx context.Context) {
	err := p.server.Shutdown(ctx)
	if err != nil {
		zap.L().Error("failed to shutdown proxy server", zap.Error(err))
	}
}

func (p *SandboxProxy) proxyHandler(transport *http.Transport) func(w http.ResponseWriter, r *http.Request) {
	activeConnections, err := meters.GetUpDownCounter(meters.OrchestratorProxyActiveConnectionsCounterMeterName)
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

		// Extract sandbox id from the host (<port>-<sandbox id>-<old client id>.e2b.dev)
		hostSplit := strings.Split(r.Host, "-")
		if len(hostSplit) < 2 {
			zap.L().Warn("invalid host to proxy", zap.String("host", r.Host))
			http.Error(w, "Invalid host", http.StatusBadRequest)
			return
		}

		sandboxID := hostSplit[1]
		sandboxPortRaw := hostSplit[0]
		sandboxPort, sandboxPortErr := strconv.ParseUint(sandboxPortRaw, 10, 64)
		if sandboxPortErr != nil {
			zap.L().Warn("invalid sandbox port", zap.String("sandbox_port", sandboxPortRaw))
			http.Error(w, "Invalid sandbox port", http.StatusBadRequest)
		}

		sbxIp, sbxFound := p.sandboxes.Get(sandboxID)
		if !sbxFound {
			zap.L().Warn("sandbox not found", zap.String("sandbox_id", sandboxID))
			http.Error(w, "Sandbox not found", http.StatusNotFound)
			return
		}

		logger := zap.L().With(zap.String("sandbox_id", sandboxID), zap.String("sandbox_ip", sbxIp), zap.Uint64("sandbox_req_port", sandboxPort), zap.String("sandbox_port_path", r.URL.Path))

		// We've resolved the node to proxy the request to
		logger.Debug("Proxying request")
		targetUrl := &url.URL{
			Scheme: "http",
			Host:   fmt.Sprintf("%s:%d", sbxIp, sandboxPort),
		}

		// Proxy the request
		proxy := httputil.NewSingleHostReverseProxy(targetUrl)
		proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
			logger.Error("Reverse proxy error", zap.Error(err))

			if p.isBrowser(r.UserAgent()) {
				res, resErr := p.buildHtmlClosedPortError(sandboxID, r.Host, sandboxPort)
				if resErr != nil {
					logger.Error("Failed to build HTML error response", zap.Error(resErr))
					w.WriteHeader(http.StatusInternalServerError)
					return
				}

				w.WriteHeader(http.StatusBadGateway)
				w.Header().Add("Content-Type", "text/html")
				w.Write(res)
				return
			}

			w.WriteHeader(http.StatusBadGateway)
			w.Header().Add("Content-Type", "application/json")
			w.Write(p.buildJsonClosedPortError(sandboxID, sandboxPort))
		}

		proxy.ModifyResponse = func(resp *http.Response) error {
			if resp.StatusCode >= 500 {
				logger.Error("Backend responded with error", zap.Int("status_code", resp.StatusCode))
			} else {
				logger.Info("Backend responded", zap.Int("status_code", resp.StatusCode))
			}

			return nil
		}

		proxy.Transport = transport
		proxy.ServeHTTP(w, r)
	}
}

func (p *SandboxProxy) buildHtmlClosedPortError(sandboxId string, host string, port uint64) ([]byte, error) {
	htmlResponse := new(bytes.Buffer)
	htmlVars := htmlTemplateData{SandboxId: sandboxId, SandboxHost: host, SandboxPort: strconv.FormatUint(port, 10)}

	err := browserTemplate.Execute(htmlResponse, htmlVars)
	if err != nil {
		return nil, err
	}

	return htmlResponse.Bytes(), nil
}

func (p *SandboxProxy) buildJsonClosedPortError(sandboxId string, port uint64) []byte {
	response := jsonTemplateData{
		Error:     "The sandbox is running but port is not open",
		SandboxId: sandboxId,
		Port:      port,
	}

	responseBytes, _ := json.Marshal(response)
	return responseBytes
}

func (p *SandboxProxy) isBrowser(userAgent string) bool {
	return browserRegex.MatchString(userAgent)
}
