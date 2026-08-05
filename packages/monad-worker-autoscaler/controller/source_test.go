package controller

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type sequenceIdentityTokenSource struct {
	tokens []string
	calls  int
	err    error
}

func (s *sequenceIdentityTokenSource) Token(_ context.Context, _ string) (string, error) {
	if s.err != nil {
		return "", s.err
	}
	token := s.tokens[s.calls]
	s.calls++

	return token, nil
}

func TestHTTPOverviewSourceMintsIdentityPerRequestWithoutURLDisclosure(t *testing.T) {
	t.Parallel()
	tokens := &sequenceIdentityTokenSource{tokens: []string{"one.payload.signature", "two.payload.signature"}}
	var requestCount atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "" || strings.Contains(r.URL.String(), "payload") {
			t.Errorf("identity token leaked into request URL: %s", r.URL.String())
		}
		index := int(requestCount.Add(1) - 1)
		want := "Bearer " + tokens.tokens[index]
		if got := r.Header.Get("Authorization"); got != want {
			t.Errorf("Authorization = %q, want %q", got, want)
		}
		_ = json.NewEncoder(w).Encode(validOverview(time.Now().UTC()))
	}))
	defer server.Close()
	source := HTTPOverviewSource{
		URL: server.URL, Audience: server.URL, TokenSource: tokens, Client: server.Client(),
	}
	for range 2 {
		if _, err := source.Fetch(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if tokens.calls != 2 || requestCount.Load() != 2 {
		t.Fatalf("expected one fresh token per request, token_calls=%d requests=%d", tokens.calls, requestCount.Load())
	}
}

func TestHTTPOverviewSourceRejectsCrossOriginBeforeMint(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests.Add(1) }))
	defer server.Close()
	tokens := &sequenceIdentityTokenSource{tokens: []string{"secret.payload.signature"}}
	source := HTTPOverviewSource{
		URL: server.URL, Audience: "https://tams.monad0.net", TokenSource: tokens, Client: server.Client(),
	}
	_, err := source.Fetch(context.Background())
	if err == nil || !strings.Contains(err.Error(), "same origin") {
		t.Fatalf("expected cross-origin rejection, got %v", err)
	}
	if tokens.calls != 0 || requests.Load() != 0 {
		t.Fatalf("cross-origin config must fail before mint or delivery, token_calls=%d requests=%d", tokens.calls, requests.Load())
	}
}

func TestHTTPOverviewSourceFailsClosedBeforeRequestWhenIdentityMintFails(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests.Add(1) }))
	defer server.Close()
	source := HTTPOverviewSource{
		URL: server.URL, Audience: server.URL,
		TokenSource: &sequenceIdentityTokenSource{err: errors.New("metadata unavailable")}, Client: server.Client(),
	}
	if _, err := source.Fetch(context.Background()); err == nil || !strings.Contains(err.Error(), "mint TAMS workload identity") {
		t.Fatalf("expected identity failure, got %v", err)
	}
	if requests.Load() != 0 {
		t.Fatalf("TAMS request must not run without identity, got %d requests", requests.Load())
	}
}

func TestHTTPOverviewSourceRedactsBearerAndResponseBodyFromErrors(t *testing.T) {
	t.Parallel()
	token := "secret.payload.signature"
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("rejected " + token))
	}))
	defer server.Close()
	source := HTTPOverviewSource{
		URL: server.URL, Audience: server.URL,
		TokenSource: &sequenceIdentityTokenSource{tokens: []string{token}}, Client: server.Client(),
	}
	_, err := source.Fetch(context.Background())
	if err == nil || strings.Contains(err.Error(), token) || strings.Contains(err.Error(), "rejected") {
		t.Fatalf("TAMS failure must not disclose bearer or body, got %v", err)
	}
}

func TestMetadataIdentityTokenSourceUsesAudienceAndRedactsFailureBody(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Metadata-Flavor") != "Google" || r.URL.Query().Get("audience") != "https://tams.monad0.net" || r.URL.Query().Get("format") != "full" {
			t.Errorf("unexpected metadata request: headers=%v query=%v", r.Header, r.URL.Query())
		}
		w.Header().Set("Metadata-Flavor", "Google")
		_, _ = w.Write([]byte("header.payload.signature\n"))
	}))
	defer server.Close()
	source := MetadataIdentityTokenSource{Endpoint: server.URL, Client: server.Client()}
	token, err := source.Token(context.Background(), "https://tams.monad0.net")
	if err != nil || token != "header.payload.signature" {
		t.Fatalf("token=%q err=%v", token, err)
	}

	failure := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("sensitive-token-body"))
	}))
	defer failure.Close()
	_, err = (MetadataIdentityTokenSource{Endpoint: failure.URL, Client: failure.Client()}).Token(context.Background(), "https://tams.monad0.net")
	if err == nil || strings.Contains(err.Error(), "sensitive-token-body") {
		t.Fatalf("metadata failure must be redacted, got %v", err)
	}
}

func TestNomadFleetSourceCountsReadyAndDrainingHosts(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Nomad-Token") != "secret" {
			t.Error("Nomad token missing")
		}
		if !strings.Contains(r.URL.Query().Get("filter"), `NodePool == "default"`) {
			t.Errorf("unexpected filter: %q", r.URL.Query().Get("filter"))
		}
		_, _ = w.Write([]byte(`[
          {"ID":"one","Name":"worker-1","Status":"ready","SchedulingEligibility":"eligible","NodePool":"default"},
          {"ID":"two","Name":"worker-2","Status":"ready","SchedulingEligibility":"ineligible","NodePool":"default"}
        ]`))
	}))
	defer server.Close()

	fleet, err := (NomadFleetSource{Address: server.URL, Token: "secret", NodePool: "default", Client: server.Client()}).Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if fleet.ActualHosts != 2 || fleet.DrainingHosts != 1 {
		t.Fatalf("unexpected fleet: %+v", fleet)
	}
}

func TestNomadFleetSourceRejectsAmbiguousNodes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		body string
		want string
	}{
		{"down", `[{"ID":"one","Name":"worker-1","Status":"down","SchedulingEligibility":"eligible","NodePool":"default"}]`, "ambiguous status"},
		{"wrong pool", `[{"ID":"one","Name":"worker-1","Status":"ready","SchedulingEligibility":"eligible","NodePool":"other"}]`, "unexpected pool"},
		{"duplicate", `[{"ID":"one","Name":"worker-1","Status":"ready","SchedulingEligibility":"eligible","NodePool":"default"},{"ID":"one","Name":"worker-2","Status":"ready","SchedulingEligibility":"eligible","NodePool":"default"}]`, "duplicate"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(test.body)) }))
			defer server.Close()
			_, err := (NomadFleetSource{Address: server.URL, Token: "secret", NodePool: "default", Client: server.Client()}).Fetch(context.Background())
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}
