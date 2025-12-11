package ioc

import "net/http"

type HealthHTTPServer struct {
	*http.Server
}
