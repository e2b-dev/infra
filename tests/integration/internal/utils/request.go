package utils

import (
	"fmt"
	"net/http"
	"net/url"

	"golang.org/x/net/publicsuffix"

	"github.com/e2b-dev/infra/tests/integration/internal/api"
)

func NewRequest(sbx *api.Sandbox, url *url.URL, port int, extraHeaders *http.Header) *http.Request {
	var host string

	if url.Hostname() == "localhost" {
		host = fmt.Sprintf("%d-%s-%s.%s", port, sbx.SandboxID, sbx.ClientID, "localhost")
	} else {
		// Extract top level domain from EnvdProxy
		eTLD, _ := publicsuffix.EffectiveTLDPlusOne(url.Hostname())
		host = fmt.Sprintf("%d-%s-%s.%s:%s", port, sbx.SandboxID, sbx.ClientID, eTLD, url.Port())
	}

	header := http.Header{
		"Host": []string{host},
	}

	if extraHeaders != nil {
		for key, values := range *extraHeaders {
			header[key] = values
		}
	}

	return &http.Request{
		Method: http.MethodGet,
		URL:    url,
		Host:   host,
		Header: header,
	}
}
