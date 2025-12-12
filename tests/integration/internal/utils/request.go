package utils

import (
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
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

	if sbx.TrafficAccessToken != nil {
		header.Set("e2b-traffic-access-token", *sbx.TrafficAccessToken)
	}

	if extraHeaders != nil {
		maps.Copy(header, *extraHeaders)
	}

	return &http.Request{
		Method: http.MethodGet,
		URL:    url,
		Host:   host,
		Header: header,
	}
}

func WaitForStatus(tb testing.TB, client *http.Client, sbx *api.Sandbox, url *url.URL, port int, headers *http.Header, expectedStatus int) *http.Response {
	tb.Helper()

	for range 10 {
		req := NewRequest(sbx, url, port, headers)
		resp, err := client.Do(req)
		if err != nil {
			tb.Logf("Error: %v", err)

			continue
		}

		if resp.StatusCode == expectedStatus {
			return resp
		}

		x, err := io.ReadAll(resp.Body)
		if err != nil {
			tb.Logf("[Status code: %d] Error reading response body: %v", resp.StatusCode, err)
		} else {
			tb.Logf("[Status code: %d] Response body: %s", resp.StatusCode, string(x))
		}
		require.NoError(tb, resp.Body.Close())

		time.Sleep(500 * time.Millisecond)
	}

	tb.Fail()

	return nil
}
