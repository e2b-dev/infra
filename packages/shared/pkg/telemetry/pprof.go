package telemetry

import (
	"fmt"
	"net/http"
	"net/http/pprof"
	"strings"
)

func init() {
	// Wrap DefaultServeMux so that /debug/pprof* paths are blocked even if
	// net/http/pprof (or anything else) registered handlers via init().
	// The original mux is preserved for non-debug paths so legitimate
	// registrations by third-party libraries still work.
	original := http.DefaultServeMux
	guarded := http.NewServeMux()
	guarded.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/debug/pprof") {
			http.NotFound(w, r)

			return
		}

		original.ServeHTTP(w, r)
	})
	http.DefaultServeMux = guarded
}

// DefaultPprofPort is the default port that we should use to mount pprof endpoint.
const DefaultPprofPort = 6060

func NewPprofServer() *http.Server {
	return &http.Server{
		// We mount only to the localhost to prevent accidental exposure.
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
