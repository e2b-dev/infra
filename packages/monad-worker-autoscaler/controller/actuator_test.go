package controller

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func newTokenMetadataServer(t *testing.T, hits *atomic.Int64, token string, expiresIn int) *httptest.Server {
	t.Helper()

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Metadata-Flavor") != "Google" {
			http.Error(w, "missing metadata flavor", http.StatusForbidden)

			return
		}
		hits.Add(1)
		w.Header().Set("Metadata-Flavor", "Google")
		fmt.Fprintf(w, `{"access_token":%q,"expires_in":%d,"token_type":"Bearer"}`, token, expiresIn)
	}))
}

func TestMetadataAccessTokenSourceMintsAndCaches(t *testing.T) {
	var hits atomic.Int64
	server := newTokenMetadataServer(t, &hits, "access-token-1", 3599)
	defer server.Close()

	now := time.Unix(1_700_000_000, 0)
	source := &MetadataAccessTokenSource{Endpoint: server.URL, Client: server.Client(), Now: func() time.Time { return now }}

	token, err := source.AccessToken(context.Background())
	if err != nil {
		t.Fatalf("mint access token: %v", err)
	}
	if token != "access-token-1" {
		t.Fatalf("unexpected token %q", token)
	}
	if _, err := source.AccessToken(context.Background()); err != nil {
		t.Fatalf("cached access token: %v", err)
	}
	if hits.Load() != 1 {
		t.Fatalf("expected one metadata request while cached, got %d", hits.Load())
	}

	now = now.Add(3599*time.Second - 30*time.Second)
	if _, err := source.AccessToken(context.Background()); err != nil {
		t.Fatalf("refresh access token: %v", err)
	}
	if hits.Load() != 2 {
		t.Fatalf("expected refresh within the expiry margin, got %d requests", hits.Load())
	}
}

func TestMetadataAccessTokenSourceRejections(t *testing.T) {
	cases := []struct {
		name    string
		handler http.HandlerFunc
	}{
		{"http error", func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "boom", http.StatusInternalServerError)
		}},
		{"missing provenance header", func(w http.ResponseWriter, _ *http.Request) {
			fmt.Fprint(w, `{"access_token":"tok","expires_in":3599,"token_type":"Bearer"}`)
		}},
		{"empty token", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Metadata-Flavor", "Google")
			fmt.Fprint(w, `{"access_token":"","expires_in":3599,"token_type":"Bearer"}`)
		}},
		{"non-bearer token", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Metadata-Flavor", "Google")
			fmt.Fprint(w, `{"access_token":"tok","expires_in":3599,"token_type":"MAC"}`)
		}},
		{"non-positive expiry", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Metadata-Flavor", "Google")
			fmt.Fprint(w, `{"access_token":"tok","expires_in":0,"token_type":"Bearer"}`)
		}},
		{"malformed body", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Metadata-Flavor", "Google")
			fmt.Fprint(w, `{"access_token"`)
		}},
		{"oversized body", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Metadata-Flavor", "Google")
			fmt.Fprintf(w, `{"access_token":%q,"expires_in":3599,"token_type":"Bearer"}`, strings.Repeat("a", 65<<10))
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(tc.handler)
			defer server.Close()
			source := &MetadataAccessTokenSource{Endpoint: server.URL, Client: server.Client()}
			if _, err := source.AccessToken(context.Background()); err == nil {
				t.Fatal("expected access-token rejection")
			}
		})
	}
}

type migServerState struct {
	targetSize   string
	resizeStatus int
	resizeCalls  atomic.Int64
	lastResize   atomic.Value
	getStatus    int
}

func newMIGServer(t *testing.T, state *migServerState) *httptest.Server {
	t.Helper()

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer access-token-1" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)

			return
		}
		basePath := "/compute/v1/projects/proj-one/regions/us-east4/instanceGroupManagers/mig-1"
		switch {
		case r.Method == http.MethodGet && r.URL.Path == basePath:
			if state.getStatus != 0 {
				http.Error(w, "boom", state.getStatus)

				return
			}
			fmt.Fprintf(w, `{"name":"mig-1","targetSize":%s}`, state.targetSize)
		case r.Method == http.MethodPost && r.URL.Path == basePath+"/resize":
			state.resizeCalls.Add(1)
			state.lastResize.Store(r.URL.Query().Get("size"))
			if state.resizeStatus != 0 {
				http.Error(w, "boom", state.resizeStatus)

				return
			}
			fmt.Fprint(w, `{"name":"operation-1","status":"RUNNING"}`)
		default:
			http.Error(w, "unexpected route "+r.Method+" "+r.URL.Path, http.StatusNotFound)
		}
	}))
}

type staticTokenSource struct{ token string }

func (s staticTokenSource) AccessToken(context.Context) (string, error) { return s.token, nil }

func newTestActuator(t *testing.T, server *httptest.Server) *GCERegionMIGActuator {
	t.Helper()
	actuator, err := NewGCERegionMIGActuator(server.Client(), staticTokenSource{token: "access-token-1"}, server.URL, "proj-one", "us-east4", "mig-1")
	if err != nil {
		t.Fatalf("construct actuator: %v", err)
	}

	return actuator
}

func TestGCERegionMIGActuatorTargetSize(t *testing.T) {
	state := &migServerState{targetSize: "4"}
	server := newMIGServer(t, state)
	defer server.Close()

	actuator := newTestActuator(t, server)
	size, err := actuator.TargetSize(context.Background())
	if err != nil {
		t.Fatalf("read target size: %v", err)
	}
	if size != 4 {
		t.Fatalf("unexpected target size %d", size)
	}

	state.targetSize = "-1"
	if _, err := actuator.TargetSize(context.Background()); err == nil {
		t.Fatal("expected negative target size rejection")
	}

	state.targetSize = "null"
	if _, err := actuator.TargetSize(context.Background()); err == nil {
		t.Fatal("expected missing target size rejection")
	}

	state.targetSize = "4"
	state.getStatus = http.StatusForbidden
	if _, err := actuator.TargetSize(context.Background()); err == nil {
		t.Fatal("expected HTTP error rejection")
	}
}

func TestGCERegionMIGActuatorResize(t *testing.T) {
	state := &migServerState{targetSize: "4"}
	server := newMIGServer(t, state)
	defer server.Close()

	actuator := newTestActuator(t, server)
	if err := actuator.Resize(context.Background(), 6); err != nil {
		t.Fatalf("resize: %v", err)
	}
	if state.resizeCalls.Load() != 1 || state.lastResize.Load() != "6" {
		t.Fatalf("expected one resize to 6, got %d calls (size %v)", state.resizeCalls.Load(), state.lastResize.Load())
	}

	for _, target := range []int{0, -1, MaximumWorkerHosts + 1} {
		if err := actuator.Resize(context.Background(), target); err == nil {
			t.Fatalf("expected out-of-bounds rejection for %d", target)
		}
	}
	if state.resizeCalls.Load() != 1 {
		t.Fatalf("out-of-bounds targets must not reach the API, got %d calls", state.resizeCalls.Load())
	}

	state.resizeStatus = http.StatusConflict
	if err := actuator.Resize(context.Background(), 6); err == nil {
		t.Fatal("expected HTTP error rejection")
	}
}

func TestNewGCERegionMIGActuatorValidation(t *testing.T) {
	client := &http.Client{}
	tokens := staticTokenSource{token: "tok"}
	cases := []struct {
		name                        string
		baseURL, project, region, mig string
	}{
		{"empty project", "https://compute.googleapis.com", "", "us-east4", "mig-1"},
		{"empty region", "https://compute.googleapis.com", "proj-one", "", "mig-1"},
		{"empty name", "https://compute.googleapis.com", "proj-one", "us-east4", ""},
		{"cleartext base", "http://compute.example.com", "proj-one", "us-east4", "mig-1"},
		{"credentialed base", "https://user@compute.googleapis.com", "proj-one", "us-east4", "mig-1"},
		{"uppercase name", "https://compute.googleapis.com", "p1", "us-east4", "MIG"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewGCERegionMIGActuator(client, tokens, tc.baseURL, tc.project, tc.region, tc.mig); err == nil {
				t.Fatal("expected constructor rejection")
			}
		})
	}
	if _, err := NewGCERegionMIGActuator(nil, tokens, "https://compute.googleapis.com", "proj-one", "us-east4", "mig-1"); err == nil {
		t.Fatal("expected nil client rejection")
	}
	if _, err := NewGCERegionMIGActuator(client, nil, "https://compute.googleapis.com", "proj-one", "us-east4", "mig-1"); err == nil {
		t.Fatal("expected nil token source rejection")
	}
}
