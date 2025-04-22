package proxy

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/meters"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
)

//go:embed proxy_browser_502.html
var proxyBrowser502PageHtml string

var browserRegex = regexp.MustCompile(`(?i)mozilla|chrome|safari|firefox|edge|opera|msie`)
var browserTemplate = template.Must(template.New("template").Parse(proxyBrowser502PageHtml))

const (
	maxConnectionDuration = 24 * time.Hour    // The same as the current max sandbox duration.
	maxIdleConnections    = 32768             // Reasonably big number that is lower than the number of available ports.
	idleTimeout           = 630 * time.Second // This should be ideally bigger than the previous downstream and lower than then next upstream timeout so the closing is not from the most upstream server.
)

type htmlTemplateData struct {
	SandboxId   string
	SandboxHost string
	SandboxPort string
}

type jsonTemplateData struct {
	Message   string `json:"message"`
	SandboxId string `json:"sandboxId"`
	Port      uint64 `json:"port"`
}

type sandboxProxyEntry struct {
	ip     string
	teamID string
}

type SandboxProxy struct {
	sandboxes *smap.Map[sandboxProxyEntry]
	server    *http.Server
}

func New(port uint) *SandboxProxy {
	server := &http.Server{
		Addr:              fmt.Sprintf(":%d", port),
		ReadTimeout:       maxConnectionDuration,
		WriteTimeout:      maxConnectionDuration,
		IdleTimeout:       idleTimeout,
		ReadHeaderTimeout: 20 * time.Second,
	}

	return &SandboxProxy{
		server:    server,
		sandboxes: smap.New[sandboxProxyEntry](),
	}
}

func (p *SandboxProxy) AddSandbox(sandboxID, ip string, teamID string) {
	p.sandboxes.Insert(sandboxID, sandboxProxyEntry{ip: ip, teamID: teamID})
}

func (p *SandboxProxy) RemoveSandbox(sandboxID string, ip string) {
	p.sandboxes.RemoveCb(sandboxID, func(k string, v sandboxProxyEntry, ok bool) bool { return ok && v.ip == ip })
}

func (p *SandboxProxy) Start() error {
	// similar values to our old the nginx configuration
	serverTransport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		MaxIdleConnsPerHost:   maxIdleConnections,
		IdleConnTimeout:       idleTimeout,
		TLSHandshakeTimeout:   20 * time.Second,
		ResponseHeaderTimeout: 20 * time.Second,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second, // Connect timeout (no timeout by default)
			KeepAlive: 30 * time.Second, // Lower than our http keepalives (50 seconds)
		}).DialContext,
		DisableCompression: true, // No need to request or manipulate compression
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

		// Extract sandbox id from the host (<port>-<sandbox id>-<old client id>.e2b.app)
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

		sbxEntry, sbxFound := p.sandboxes.Get(sandboxID)
		if !sbxFound {
			zap.L().Warn("sandbox not found", zap.String("sandbox_id", sandboxID))
			http.Error(w, "Sandbox not found", http.StatusNotFound)
			return
		}

		logger := zap.L().With(
			zap.String("sandbox_id", sandboxID),
			zap.String("sandbox_ip", sbxEntry.ip),
			zap.String("team_id", sbxEntry.teamID),
			zap.Uint64("sandbox_req_port", sandboxPort),
			zap.String("sandbox_port_path", r.URL.Path),
		)

		// We've resolved the node to proxy the request to
		logger.Debug("Proxying request")
		targetUrl := &url.URL{
			Scheme: "http",
			Host:   fmt.Sprintf("%s:%d", sbxEntry.ip, sandboxPort),
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

		proxyLogger, _ := zap.NewStdLogAt(logger, zap.ErrorLevel)
		proxy.ErrorLog = proxyLogger
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
		Message:   "The sandbox is running but port is not open",
		SandboxId: sandboxId,
		Port:      port,
	}

	responseBytes, _ := json.Marshal(response)
	return responseBytes
}

func (p *SandboxProxy) isBrowser(userAgent string) bool {
	return browserRegex.MatchString(userAgent)
}
