package httpserver

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/net/http2"
)

func TestWithH2CAcceptsHTTP2AndHTTP1(t *testing.T) {
	handler := WithH2C(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	server := httptest.NewServer(handler)
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

	h1Resp, err := server.Client().Get(server.URL)
	require.NoError(t, err)
	defer h1Resp.Body.Close()

	require.Equal(t, http.StatusNoContent, h1Resp.StatusCode)
	require.Equal(t, "HTTP/1.1", h1Resp.Proto)
}
