package storage

import (
	"net/http"

	"go.uber.org/zap"
)

type proxyTransport struct {
	baseTransport *http.Transport
	proxyHost     string
}

func newProxyTransport(proxyHost string) *proxyTransport {
	return &proxyTransport{
		baseTransport: &http.Transport{},
		proxyHost:     proxyHost,
	}
}

// RoundTrip implements http.RoundTripper
func (p *proxyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.URL.Host = p.proxyHost
	req.URL.Scheme = "http"

	zap.L().Debug("Forwarding", zap.String("host", req.URL.Host), zap.String("path", req.URL.Path), zap.String("range", req.Header.Get("Range")))

	return p.baseTransport.RoundTrip(req)
}
