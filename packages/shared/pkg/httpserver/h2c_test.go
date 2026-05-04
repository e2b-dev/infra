package httpserver

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/net/http2"
)

func TestConfigureH2CAcceptsHTTP2AndHTTP1(t *testing.T) {
	t.Parallel()

	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	server := httptest.NewUnstartedServer(nil)
	ConfigureH2C(server.Config, handler)
	server.Start()
	t.Cleanup(server.Close)

	h2Client := &http.Client{
		Transport: &http2.Transport{
			AllowHTTP: true,
			DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, addr)
			},
		},
	}

	h2Req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL, nil)
	require.NoError(t, err)

	h2Resp, err := h2Client.Do(h2Req)
	require.NoError(t, err)
	defer h2Resp.Body.Close()

	require.Equal(t, http.StatusNoContent, h2Resp.StatusCode)
	require.Equal(t, "HTTP/2.0", h2Resp.Proto)

	h1Req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL, nil)
	require.NoError(t, err)

	h1Resp, err := server.Client().Do(h1Req)
	require.NoError(t, err)
	defer h1Resp.Body.Close()

	require.Equal(t, http.StatusNoContent, h1Resp.StatusCode)
	require.Equal(t, "HTTP/1.1", h1Resp.Proto)
}

func TestConfigureH2CLimitsUpgradeRequestBodyOnly(t *testing.T) {
	t.Parallel()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := io.Copy(io.Discard, r.Body)
		if err != nil {
			t.Errorf("copy request body: %v", err)

			return
		}

		w.WriteHeader(http.StatusNoContent)
	})

	server := httptest.NewUnstartedServer(nil)
	ConfigureH2C(server.Config, handler)
	server.Start()
	t.Cleanup(server.Close)

	h1Req, err := http.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		server.URL,
		strings.NewReader(strings.Repeat("a", h2cUpgradeBodyLimit+1)),
	)
	require.NoError(t, err)

	h1Resp, err := server.Client().Do(h1Req)
	require.NoError(t, err)
	defer h1Resp.Body.Close()

	require.Equal(t, http.StatusNoContent, h1Resp.StatusCode)

	upgradeReq, err := http.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		server.URL,
		struct{ io.Reader }{strings.NewReader(strings.Repeat("a", h2cUpgradeBodyLimit+1))},
	)
	require.NoError(t, err)
	upgradeReq.Header.Set("Connection", "Upgrade, HTTP2-Settings")
	upgradeReq.Header.Set("HTTP2-Settings", "AAMAAABkAAQAAP__")
	upgradeReq.Header.Set("Upgrade", "h2c")

	upgradeResp, err := server.Client().Do(upgradeReq)
	require.NoError(t, err)
	defer upgradeResp.Body.Close()

	require.Equal(t, http.StatusInternalServerError, upgradeResp.StatusCode)
}

func TestNewHTTP2ServerUsesParentIdleTimeout(t *testing.T) {
	t.Parallel()

	const parentIdleTimeout = 620 * time.Second

	h2Server := newHTTP2Server(&http.Server{IdleTimeout: parentIdleTimeout})

	require.Equal(t, parentIdleTimeout, h2Server.IdleTimeout)
	require.Equal(t, uint32(100), h2Server.MaxConcurrentStreams)
	require.Equal(t, 30*time.Second, h2Server.ReadIdleTimeout)
}

func TestNewHTTP2ServerUsesDefaultIdleTimeout(t *testing.T) {
	t.Parallel()

	h2Server := newHTTP2Server(&http.Server{})

	require.Equal(t, defaultH2CIdleTimeout, h2Server.IdleTimeout)
}
