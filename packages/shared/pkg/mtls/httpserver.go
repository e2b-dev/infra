package mtls

import (
	"net"
	"net/http"
)

// WrapHTTPServer puts s behind a Listener on inner: s.Handler is wrapped in
// the guard, built on cfg with healthPaths and the listener's connection
// lookup. Call it before any h2c wrapping of s, so every h2c stream passes
// the guard, then serve the returned Listener.
func WrapHTTPServer(s *http.Server, inner net.Listener, cfg ServerConfig, healthPaths []string, opts ...ListenerOption) *Listener {
	l := NewListener(inner, cfg, opts...)
	handler := s.Handler
	if handler == nil {
		handler = http.DefaultServeMux
	}
	s.Handler = NewHTTPGuard(cfg, healthPaths, handler, WithGuardConnStateLookup(l.ConnState))

	return l
}
