package auth

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/auth/pkg/auth/oidc"
)

type countingIdentityLookup struct {
	mu      sync.Mutex
	calls   atomic.Int32
	results map[string]struct {
		id  uuid.UUID
		err error
	}
}

func (l *countingIdentityLookup) GetUserIdentity(_ context.Context, iss, sub string) (uuid.UUID, error) {
	l.calls.Add(1)
	l.mu.Lock()
	defer l.mu.Unlock()
	res, ok := l.results[iss+"|"+sub]
	if !ok {
		return uuid.Nil, oidc.ErrIdentityNotFound
	}

	return res.id, res.err
}

func TestCachingIdentityLookup_CachesSuccess(t *testing.T) {
	t.Parallel()

	userID := uuid.New()
	base := &countingIdentityLookup{results: map[string]struct {
		id  uuid.UUID
		err error
	}{
		"iss|sub": {id: userID},
	}}
	lookup := newCachingIdentityLookup(base)

	for range 5 {
		got, err := lookup.GetUserIdentity(t.Context(), "iss", "sub")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != userID {
			t.Fatalf("expected %v, got %v", userID, got)
		}
	}

	if got := base.calls.Load(); got != 1 {
		t.Fatalf("expected 1 underlying call, got %d", got)
	}
}

func TestCachingIdentityLookup_DoesNotCacheNotFound(t *testing.T) {
	t.Parallel()

	base := &countingIdentityLookup{results: map[string]struct {
		id  uuid.UUID
		err error
	}{}}
	lookup := newCachingIdentityLookup(base)

	for range 3 {
		_, err := lookup.GetUserIdentity(t.Context(), "iss", "sub")
		if !errors.Is(err, oidc.ErrIdentityNotFound) {
			t.Fatalf("expected ErrIdentityNotFound, got %v", err)
		}
	}

	if got := base.calls.Load(); got != 3 {
		t.Fatalf("expected 3 underlying calls (no negative caching), got %d", got)
	}
}

func TestCachingIdentityLookup_DoesNotCacheError(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("transient db error")
	base := &countingIdentityLookup{results: map[string]struct {
		id  uuid.UUID
		err error
	}{
		"iss|sub": {err: sentinel},
	}}
	lookup := newCachingIdentityLookup(base)

	for range 3 {
		_, err := lookup.GetUserIdentity(t.Context(), "iss", "sub")
		if !errors.Is(err, sentinel) {
			t.Fatalf("expected sentinel error, got %v", err)
		}
	}

	if got := base.calls.Load(); got != 3 {
		t.Fatalf("expected 3 underlying calls (no error caching), got %d", got)
	}
}

func TestCachingIdentityLookup_SeparateKeysPerIssuerAndSubject(t *testing.T) {
	t.Parallel()

	a, b := uuid.New(), uuid.New()
	base := &countingIdentityLookup{results: map[string]struct {
		id  uuid.UUID
		err error
	}{
		"iss-a|sub": {id: a},
		"iss-b|sub": {id: b},
	}}
	lookup := newCachingIdentityLookup(base)

	got, err := lookup.GetUserIdentity(t.Context(), "iss-a", "sub")
	if err != nil || got != a {
		t.Fatalf("iss-a: got %v, %v; want %v", got, err, a)
	}

	got, err = lookup.GetUserIdentity(t.Context(), "iss-b", "sub")
	if err != nil || got != b {
		t.Fatalf("iss-b: got %v, %v; want %v", got, err, b)
	}

	if got := base.calls.Load(); got != 2 {
		t.Fatalf("expected 2 underlying calls, got %d", got)
	}
}
