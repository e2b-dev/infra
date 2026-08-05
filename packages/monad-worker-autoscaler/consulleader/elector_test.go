package consulleader

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestElectorUsesConsulSessionLock(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	owner := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Consul-Token") != "consul-secret" {
			t.Error("Consul token missing")
		}
		switch r.URL.Path {
		case "/v1/session/create":
			_, _ = w.Write([]byte(`{"ID":"session-one"}`))
		case "/v1/kv/service/monad-worker-autoscaler/leader":
			mu.Lock()
			defer mu.Unlock()
			session := r.URL.Query().Get("acquire")
			acquired := owner == "" || owner == session
			if acquired {
				owner = session
			}
			_ = json.NewEncoder(w).Encode(acquired)
		case "/v1/session/destroy/session-one":
			mu.Lock()
			owner = ""
			mu.Unlock()
			_, _ = w.Write([]byte("true"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	elector := &Elector{
		Address: server.URL, Token: "consul-secret", LockKey: "service/monad-worker-autoscaler/leader",
		InstanceID: "allocation-one", TTL: 30 * time.Second, Client: server.Client(),
	}
	leader, err := elector.Observe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !leader {
		t.Fatal("expected allocation to acquire lock")
	}
	if err := elector.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestElectorFailsClosedWhenLockIsOwned(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/session/create":
			_, _ = w.Write([]byte(`{"ID":"session-two"}`))
		case "/v1/kv/lock":
			_, _ = w.Write([]byte("false"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	elector := &Elector{Address: server.URL, Token: "token", LockKey: "lock", InstanceID: "two", TTL: 30 * time.Second, Client: server.Client()}
	leader, err := elector.Observe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if leader {
		t.Fatal("lock refusal must not be treated as leadership")
	}
}
