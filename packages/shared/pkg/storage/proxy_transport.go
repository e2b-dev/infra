package storage

import (
	"fmt"
	"net/http"
	"os"

	"sync/atomic"
)

type proxyTransport struct {
	baseTransport *http.Transport
	forwarding    *atomic.Bool
	proxyHost     string
}

func newProxyTransport(proxyHost string) *proxyTransport {
	forwarding := atomic.Bool{}
	forwarding.Store(true)

	return &proxyTransport{
		baseTransport: &http.Transport{},
		forwarding:    &forwarding,
		proxyHost:     proxyHost,
	}
}

// RoundTrip implements http.RoundTripper
func (p *proxyTransport) RoundTrip(req *http.Request) (*http.Response, error) {

	if !p.forwarding.Load() {
		fmt.Fprintf(os.Stderr, "Not forwarding: %s, %s\n", req.URL.Host, req.URL.Path)

		return p.baseTransport.RoundTrip(req)
	}

	req.URL.Host = p.proxyHost
	req.URL.Scheme = "http"

	fmt.Fprintf(os.Stderr, "Forwarding: %s, %s, %+v\n", req.URL.Host, req.URL.Path, req.Header.Get("Range"))

	return p.baseTransport.RoundTrip(req)

	// // Create a new request with the proxy host
	// proxyURL := &url.URL{
	// 	Scheme:      "http",
	// 	Host:        p.proxyHost,
	// 	Path:        req.URL.Path,
	// 	RawQuery:    req.URL.RawQuery,
	// 	Fragment:    req.URL.Fragment,
	// 	User:        req.URL.User,
	// 	RawPath:     req.URL.RawPath,
	// 	Opaque:      req.URL.Opaque,
	// 	ForceQuery:  req.URL.ForceQuery,
	// 	RawFragment: req.URL.RawFragment,
	// }

	// // Create a new request with the proxy URL
	// newReq, err := http.NewRequest(req.Method, proxyURL.String(), req.Body)
	// if err != nil {
	// 	return nil, err
	// }

	// // Copy headers
	// newReq.Header = maps.Clone(req.Header)

	// fmt.Fprintf(os.Stderr, "Forwarding: %s, %s\n", newReq.URL.Host, newReq.URL.Path)

	// // Use the base transport to make the request
	// return p.baseTransport.RoundTrip(newReq)
}

// DisableForwarding disables the forwarding for the transport
func (p *proxyTransport) DisableForwarding() {
	if p.forwarding.CompareAndSwap(true, false) {
		fmt.Fprintf(os.Stderr, "Disabling forwarding\n")
	}
}
