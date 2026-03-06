package telemetry

import (
	"fmt"
	"net/http"
	"net/http/pprof"
)

func init() {
	// Replace DefaultServeMux so that any past or future blank import of
	// net/http/pprof (which registers handlers in its own init()) has no
	// effect on the mux actually served by http.ListenAndServe(addr, nil).
	//
	// In some cases this might wipe the default serve mux, but we should not use the default serve mux anywhere.
	http.DefaultServeMux = http.NewServeMux()
}

// DefaultPprofPort is the default port that we should use to mount pprof endpoint.
const DefaultPprofPort = 6060

func NewPprofServer() *http.Server {
	return &http.Server{
		// We mount only to the local host, so we don't expose it to the outside world.
		Addr:    fmt.Sprintf("127.0.0.1:%d", DefaultPprofPort),
		Handler: NewPprofMux(),
	}
}

// NewPprofMux returns a ServeMux with pprof handlers registered on a dedicated
// mux rather than http.DefaultServeMux, preventing accidental exposure.
//
// Mirrors the five handlers that `_ "net/http/pprof"` registers in its init().
// The Index handler already serves all runtime/pprof profiles (heap, goroutine,
// allocs, block, mutex, threadcreate, etc.) by name from the URL path.
func NewPprofMux() *http.ServeMux {
	mux := http.NewServeMux()

	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)

	return mux
}
