package testhacks

import "net/http"

func ProcessHTTPRequest(r *http.Request) *http.Request {
	if !IsTesting() {
		return r
	}

	testName := r.Header.Get("X-Test-Name")
	if testName == "" {
		return r
	}

	ctx := r.Context()
	ctx = addTestName(ctx, testName)
	r = r.WithContext(ctx)

	return r
}
