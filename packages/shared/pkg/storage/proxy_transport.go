package storage

import (
	"fmt"
	"net/http"
	"net/url"
	"os"

	"sync/atomic"
)

type proxyTransport struct {
	*http.Transport
	proxying  *atomic.Bool
	proxyHost string
}

func newProxyTransport(proxyHost string) *proxyTransport {
	proxying := atomic.Bool{}
	proxying.Store(true)

	t := &http.Transport{
		Proxy: func(req *http.Request) (*url.URL, error) {
			fmt.Fprintf(os.Stderr, "Proxy: %s, %s\n", req.URL.Host, req.URL.Path)
			if proxying.Load() {
				return &url.URL{
					Scheme:      "http",
					Host:        proxyHost,
					Path:        req.URL.Path,
					RawQuery:    req.URL.RawQuery,
					Fragment:    req.URL.Fragment,
					User:        req.URL.User,
					RawPath:     req.URL.RawPath,
					Opaque:      req.URL.Opaque,
					ForceQuery:  req.URL.ForceQuery,
					RawFragment: req.URL.RawFragment,
				}, nil
			}

			return nil, nil
		},
	}

	return &proxyTransport{
		Transport: t,
		proxying:  &proxying,
		proxyHost: proxyHost,
	}
}

// DisableProxy disables the proxy for the transport
func (p *proxyTransport) disableProxy() {
	p.proxying.CompareAndSwap(true, false)
}
