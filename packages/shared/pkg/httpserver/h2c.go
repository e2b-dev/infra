package httpserver

import (
	"net/http"
	"strings"
	"time"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

const (
	defaultH2CIdleTimeout = 650 * time.Second
	h2cUpgradeBodyLimit   = 1 << 20 // 1 MiB
)

// ConfigureH2C wraps server's handler with H2C support using server timeouts.
func ConfigureH2C(server *http.Server) {
	handler := server.Handler
	if handler == nil {
		handler = http.DefaultServeMux
	}

	server.Handler = withH2C(server, handler)
}

func withH2C(server *http.Server, handler http.Handler) http.Handler {
	h2cHandler := h2c.NewHandler(handler, newHTTP2Server(server))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isH2CUpgrade(r.Header) {
			http.MaxBytesHandler(h2cHandler, h2cUpgradeBodyLimit).ServeHTTP(w, r)

			return
		}

		h2cHandler.ServeHTTP(w, r)
	})
}

func newHTTP2Server(server *http.Server) *http2.Server {
	idleTimeout := defaultH2CIdleTimeout
	if server != nil && server.IdleTimeout > 0 {
		idleTimeout = server.IdleTimeout
	}

	return &http2.Server{
		MaxConcurrentStreams:         100,
		IdleTimeout:                  idleTimeout,
		ReadIdleTimeout:              30 * time.Second,
		PingTimeout:                  15 * time.Second,
		WriteByteTimeout:             30 * time.Second,
		MaxUploadBufferPerConnection: 1 << 20,
		MaxUploadBufferPerStream:     1 << 20,
	}
}

func isH2CUpgrade(header http.Header) bool {
	return strings.EqualFold(header.Get("Upgrade"), "h2c") &&
		header.Get("HTTP2-Settings") != "" &&
		headerValueContainsToken(header.Get("Connection"), "Upgrade") &&
		headerValueContainsToken(header.Get("Connection"), "HTTP2-Settings")
}

func headerValueContainsToken(value, token string) bool {
	for field := range strings.SplitSeq(value, ",") {
		if strings.EqualFold(strings.TrimSpace(field), token) {
			return true
		}
	}

	return false
}
