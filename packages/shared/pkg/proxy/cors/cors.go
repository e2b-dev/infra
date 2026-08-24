// Package cors adds CORS headers to responses the proxy synthesizes itself.
// Responses produced by a live sandbox get theirs from envd or from the user's
// own server; this package is only for the responses we generate on their behalf.
package cors

import "net/http"

// SetHeaders marks a response as readable by browser JS from any origin.
// No Vary: Origin — the value is constant, so varying on it only fragments caches.
// No Allow-Credentials — mutually exclusive with "*", and these paths use no cookies.
func SetHeaders(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
}

// Error replies like http.Error, plus the header without which a browser hides
// the status and body from JS.
func Error(w http.ResponseWriter, message string, code int) {
	SetHeaders(w)
	http.Error(w, message, code)
}

// IsPreflight reports whether r is a CORS preflight. A bare OPTIONS is an
// ordinary request and must be answered as one.
func IsPreflight(r *http.Request) bool {
	return r.Method == http.MethodOptions &&
		r.Header.Get("Access-Control-Request-Method") != ""
}

// HandlePreflight answers a preflight and reports whether it did. Use ONLY where
// no upstream can answer — a preflight answered on a live sandbox's behalf tells
// the browser to dispatch requests that server never agreed to receive.
func HandlePreflight(w http.ResponseWriter, r *http.Request) bool {
	if !IsPreflight(r) {
		return false
	}

	SetHeaders(w)
	w.Header().Set("Access-Control-Allow-Methods", "*")
	// Echo what the browser asked for: the CORS-safelist is fixed by the Fetch
	// standard, so a server cannot opt out of preflights for its own headers —
	// X-Access-Token, Connect-Protocol-Version and the routing headers all force one.
	if h := r.Header.Get("Access-Control-Request-Headers"); h != "" {
		w.Header().Set("Access-Control-Allow-Headers", h)
	}
	w.Header().Set("Access-Control-Max-Age", "86400") // browsers clamp; Chrome caps at 2h
	w.WriteHeader(http.StatusNoContent)

	return true
}
