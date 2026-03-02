package telemetry

import (
	"net/http"
	"net/http/pprof"
)

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
