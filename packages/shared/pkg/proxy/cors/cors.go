// Package cors adds CORS headers to responses the proxy synthesizes itself.
//
// Responses produced by a live sandbox get their CORS headers from envd. When
// the sandbox is gone, or the proxy cannot reach it, the proxy answers instead —
// and without these headers the browser hands JS an opaque network error rather
// than the status and body we generated.
package cors

import "net/http"

// preflightMaxAge is how long a browser may cache a preflight result. Browsers
// clamp this to their own ceiling (Chrome caps it at 2 hours).
const preflightMaxAge = "86400"

// SetHeaders marks a response as readable by browser JS from any origin.
//
// These endpoints do not rely on cookies, so no Access-Control-Allow-Credentials
// is sent; note that credentialed requests and the "*" origin are mutually
// exclusive if that ever changes.
func SetHeaders(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Add("Vary", "Origin")
}

// Error replies with a plain-text error like http.Error, plus the CORS headers
// without which a browser hides the status and body from JS.
func Error(w http.ResponseWriter, message string, code int) {
	SetHeaders(w)
	http.Error(w, message, code)
}

// isPreflight reports whether r is a CORS preflight request. A bare OPTIONS is
// an ordinary request and has to be answered as one.
func isPreflight(r *http.Request) bool {
	return r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != ""
}

// HandlePreflight answers a CORS preflight request and reports whether it did.
//
// A browser only honors a preflight response with an ok status, so an error
// response can never satisfy one: the browser rejects the preflight and never
// sends the real request, hiding the very error we were trying to report. Use
// this on paths that would otherwise answer with an error status.
func HandlePreflight(w http.ResponseWriter, r *http.Request) bool {
	if !isPreflight(r) {
		return false
	}

	SetHeaders(w)
	w.Header().Set("Access-Control-Allow-Methods", "*")
	// Echo whatever the browser asked for. The CORS-safelist is fixed by the
	// Fetch standard, so a server cannot opt out of preflights for its own
	// headers — X-Access-Token, Connect-Protocol-Version and the routing headers
	// all force one.
	if headers := r.Header.Get("Access-Control-Request-Headers"); headers != "" {
		w.Header().Set("Access-Control-Allow-Headers", headers)
	}
	w.Header().Set("Access-Control-Max-Age", preflightMaxAge)
	w.WriteHeader(http.StatusNoContent)

	return true
}
