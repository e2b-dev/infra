package main

import "testing"

//nolint:paralleltest // uses t.Setenv, which is incompatible with t.Parallel
func TestProxyPort(t *testing.T) {
	t.Setenv("PROXY_PORT", "")
	if got := proxyPort(); got != 5007 {
		t.Fatalf("unset: got %d, want 5007", got)
	}

	t.Setenv("PROXY_PORT", "6007")
	if got := proxyPort(); got != 6007 {
		t.Fatalf("valid override: got %d, want 6007", got)
	}

	t.Setenv("PROXY_PORT", "not-a-port")
	if got := proxyPort(); got != 5007 {
		t.Fatalf("invalid: got %d, want 5007 (fallback)", got)
	}
}
