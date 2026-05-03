package httpserver

import (
	"net/http"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

func WithH2C(handler http.Handler) http.Handler {
	return h2c.NewHandler(handler, &http2.Server{})
}
