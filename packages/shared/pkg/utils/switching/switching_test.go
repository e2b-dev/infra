package switching

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/launchdarkly/go-sdk-common/v3/ldvalue"
	"github.com/launchdarkly/go-server-sdk/v7/testhelpers/ldtestdata"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/metric/noop"

	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
)

type fakeResource struct {
	id         string
	closeCalls atomic.Int32
}

func (f *fakeResource) Close(context.Context) error {
	f.closeCalls.Add(1)

	return nil
}

func newTestFeatureFlags(t *testing.T) (*featureflags.Client, *ldtestdata.TestDataSource) {
	t.Helper()
	source := ldtestdata.DataSource()
	ff, err := featureflags.NewClientWithDatasource(source)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ff.Close(context.Background()) })

	return ff, source
}

func setFlag(t *testing.T, source *ldtestdata.TestDataSource, key string, value string) {
	t.Helper()
	source.Update(source.Flag(key).ValueForAll(ldvalue.String(value)))
}

func TestNew_Validation(t *testing.T) {
	t.Parallel()
	ff, _ := newTestFeatureFlags(t)
	flag := featureflags.NewStringFlag("test", "")
	factory := func(dsn string) (*fakeResource, error) { return &fakeResource{id: dsn}, nil }

	tests := []struct {
		name    string
		ff      *featureflags.Client
		factory Factory[*fakeResource]
		dsn     string
		opts    []Option[*fakeResource]
		wantErr string
	}{
		{"missing ff", nil, factory, "default", nil, "requires a feature flags client"},
		{"missing factory", ff, nil, "default", nil, "requires a client factory"},
		{"missing dsn", ff, factory, "", nil, "default DSN is required"},
		{"blank dsn", ff, factory, "  ", nil, "default DSN is required"},
		{"noop allowed but no factory", ff, factory, "", []Option[*fakeResource]{WithAllowNoopDefault[*fakeResource](true)}, "no noopFactory was provided"},
		{"negative warn cap", ff, factory, "default", []Option[*fakeResource]{WithWarnCap[*fakeResource](-1)}, "must not be negative"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := New(t.Context(), tt.ff, flag, tt.dsn, nil, tt.factory, tt.opts...)
			require.Error(t, err)
			require.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestNew_AlternateFactoryFailures(t *testing.T) {
	t.Parallel()
	ff, source := newTestFeatureFlags(t)
	flag := featureflags.NewStringFlag("test", "")

	// Test case: one alternate returns error, one returns typed nil.
	// Both should be skipped and Resolve should fall back to default.
	s, err := New[*fakeResource](t.Context(), ff, flag, "default", []string{"good", "bad-err", "typed-nil"}, func(dsn string) (*fakeResource, error) {
		switch dsn {
		case "bad-err":
			return nil, errors.New("factory error")
		case "typed-nil":
			var nilResource *fakeResource

			return nilResource, nil
		default:
			return &fakeResource{id: dsn}, nil
		}
	})
	require.NoError(t, err)
	require.Len(t, s.alternates, 3)
	require.NotNil(t, s.alternates[0])
	require.Nil(t, s.alternates[1])
	require.Nil(t, s.alternates[2])
	require.True(t, s.alternateNonNil[0])
	require.False(t, s.alternateNonNil[1])
	require.False(t, s.alternateNonNil[2])

	ctx := context.Background()

	// Should fall back to default for skipped alternates
	setFlag(t, source, flag.Key(), "1") // bad-err
	require.Equal(t, "default", s.Resolve(ctx).id)

	setFlag(t, source, flag.Key(), "2") // typed-nil
	require.Equal(t, "default", s.Resolve(ctx).id)
}

func TestSwitcher_DSNInvariants(t *testing.T) {
	t.Parallel()
	ff, _ := newTestFeatureFlags(t)
	flag := featureflags.NewStringFlag("test", "")

	// Test case: Trimming and Cloning
	dsns := []string{"  alt-0  ", "alt-1"}
	s, err := New[*fakeResource](t.Context(), ff, flag, "  default  ", dsns, func(dsn string) (*fakeResource, error) {
		return &fakeResource{id: dsn}, nil
	})
	require.NoError(t, err)

	// Mutate caller slice - should not affect switcher (cloning check)
	dsns[1] = "mutated"

	require.Equal(t, "default", s.defaultClient.id)
	require.Equal(t, "alt-0", s.alternates[0].id)
	require.Equal(t, "alt-1", s.alternates[1].id)
}

func TestSwitcher_Resolve_EdgeCases(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		flagVal  string
		expected string
		warns    int64
	}{
		{"empty", "", "default", 0},
		{"valid with space", " 0 ", "alt-0", 0},
		{"out of range", "99", "default", 1},
		{"negative", "-1", "default", 1},
		{"non-numeric", "bad", "default", 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ff, source := newTestFeatureFlags(t)
			flag := featureflags.NewStringFlag("test", "")

			s, err := New[*fakeResource](t.Context(), ff, flag, "default", []string{"alt-0"}, func(dsn string) (*fakeResource, error) {
				return &fakeResource{id: dsn}, nil
			})
			require.NoError(t, err)

			ctx := context.Background()
			setFlag(t, source, flag.Key(), tt.flagVal)
			require.Equal(t, tt.expected, s.Resolve(ctx).id)
			require.Equal(t, tt.warns, s.warnCount.Load())
		})
	}
}

func TestSwitcher_WarnCap_Deduplication(t *testing.T) {
	t.Parallel()
	ff, source := newTestFeatureFlags(t)
	flag := featureflags.NewStringFlag("test", "")

	s, err := New[*fakeResource](t.Context(), ff, flag, "default", nil, func(dsn string) (*fakeResource, error) {
		return &fakeResource{id: dsn}, nil
	}, WithWarnCap[*fakeResource](2))
	require.NoError(t, err)

	ctx := context.Background()

	// Warn 1
	setFlag(t, source, flag.Key(), "bad-1")
	s.Resolve(ctx)
	require.Equal(t, int64(1), s.warnCount.Load())

	// Same value again - should not increment count
	s.Resolve(ctx)
	require.Equal(t, int64(1), s.warnCount.Load())

	// Warn 2
	setFlag(t, source, flag.Key(), "bad-2")
	s.Resolve(ctx)
	require.Equal(t, int64(2), s.warnCount.Load())

	// Warn 3 - capped
	setFlag(t, source, flag.Key(), "bad-3")
	s.Resolve(ctx)
	require.Equal(t, int64(2), s.warnCount.Load())
}

func TestSwitcher_Sanitization(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		err         string
		contains    []string
		notContains []string
	}{
		{
			name:     "nil",
			err:      "",
			contains: nil,
		},
		{
			name:     "simple",
			err:      "failed to connect",
			contains: []string{"failed to connect"},
		},
		{
			name:        "password in params",
			err:         "host=localhost password=secret port=5432",
			contains:    []string{"password=<redacted>"},
			notContains: []string{"secret"},
		},
		{
			name:        "userinfo in url",
			err:         "clickhouse://user:pass@localhost:9000/db",
			contains:    []string{"clickhouse://<redacted>"},
			notContains: []string{"user", "pass"},
		},
		{
			name:        "full dsn",
			err:         "error with clickhouse://localhost:9000?user=default",
			contains:    []string{"clickhouse://<redacted>"},
			notContains: []string{"default"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if tt.name == "nil" {
				require.NoError(t, sanitizeDriverErr(nil))

				return
			}
			got := sanitizeDriverErr(errors.New(tt.err)).Error()
			for _, c := range tt.contains {
				require.Contains(t, got, c)
			}
			for _, nc := range tt.notContains {
				require.NotContains(t, got, nc)
			}
		})
	}
}

func TestSwitcher_Close_Idempotence(t *testing.T) {
	t.Parallel()
	ff, _ := newTestFeatureFlags(t)
	flag := featureflags.NewStringFlag("test", "")
	def := &fakeResource{id: "def"}

	s, err := New[*fakeResource](t.Context(), ff, flag, "", nil, func(string) (*fakeResource, error) { return nil, nil }, WithDefaultClient[*fakeResource](def))
	require.NoError(t, err)

	ctx := context.Background()
	require.NoError(t, s.Close(ctx))
	require.Equal(t, int32(1), def.closeCalls.Load())

	// Second call should be no-op
	require.NoError(t, s.Close(ctx))
	require.Equal(t, int32(1), def.closeCalls.Load())
}

func TestSwitcher_WithDefaultClient(t *testing.T) {
	t.Parallel()
	ff, _ := newTestFeatureFlags(t)
	flag := featureflags.NewStringFlag("test-flag", "")
	def := &fakeResource{id: "explicit-default"}

	s, err := New[*fakeResource](
		t.Context(), ff, flag, "", nil,
		func(string) (*fakeResource, error) { return nil, errors.New("factory should not be called") },
		WithDefaultClient[*fakeResource](def),
	)
	require.NoError(t, err)
	require.Equal(t, def, s.Resolve(context.Background()))

	// P1: Test WithDefaultClient(nil) vulnerability
	_, err = New[*fakeResource](
		t.Context(), ff, flag, "", nil,
		func(string) (*fakeResource, error) { return nil, nil },
		WithDefaultClient[*fakeResource](nil),
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "provided default client must not be nil")
}

func TestSwitcher_WithMeter(t *testing.T) {
	t.Parallel()
	ff, source := newTestFeatureFlags(t)
	flag := featureflags.NewStringFlag("test", "")

	meter := noop.NewMeterProvider().Meter("github.com/e2b-dev/infra/packages/shared/pkg/utils/switching")
	s, err := New[*fakeResource](t.Context(), ff, flag, "default", nil, func(dsn string) (*fakeResource, error) {
		return &fakeResource{id: dsn}, nil
	}, WithMeter[*fakeResource](meter))
	require.NoError(t, err)
	require.NotNil(t, s.resolveCounter)

	// Exercise Resolve with metrics enabled
	setFlag(t, source, flag.Key(), "")
	s.Resolve(context.Background())
}

func TestSwitcher_RecoveryAfterInvalid(t *testing.T) {
	t.Parallel()
	ff, source := newTestFeatureFlags(t)
	flag := featureflags.NewStringFlag("test-flag", "")

	s, err := New[*fakeResource](t.Context(), ff, flag, "default", []string{"alt-0"}, func(dsn string) (*fakeResource, error) {
		return &fakeResource{id: dsn}, nil
	})
	require.NoError(t, err)

	ctx := context.Background()

	// 1. Start with invalid
	setFlag(t, source, flag.Key(), "bad")
	require.Equal(t, "default", s.Resolve(ctx).id)

	// 2. Switch to valid - should recover immediately
	setFlag(t, source, flag.Key(), "0")
	require.Equal(t, "alt-0", s.Resolve(ctx).id)

	// 3. Switch back to invalid
	setFlag(t, source, flag.Key(), "99")
	require.Equal(t, "default", s.Resolve(ctx).id)

	// 4. Switch to valid again
	setFlag(t, source, flag.Key(), "0")
	require.Equal(t, "alt-0", s.Resolve(ctx).id)
}

func TestSwitcher_Concurrency_LiveUpdates(t *testing.T) {
	t.Parallel()
	ff, source := newTestFeatureFlags(t)
	flag := featureflags.NewStringFlag("test", "")

	alts := make([]string, 10)
	for i := range 10 {
		alts[i] = fmt.Sprintf("alt-%d", i)
	}

	s, err := New[*fakeResource](t.Context(), ff, flag, "default", alts, func(dsn string) (*fakeResource, error) {
		return &fakeResource{id: dsn}, nil
	})
	require.NoError(t, err)

	var wg sync.WaitGroup
	ctx := context.Background()

	// Hammer resolve
	for i := range 20 {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for range 500 {
				res := s.Resolve(ctx)
				assert.NotNil(t, res)
				if id%2 == 0 {
					// Some readers should see updates quickly
					_ = res.id
				}
			}
		}(i)
	}

	// Mutate flag simultaneously
	wg.Go(func() {
		for i := range 100 {
			setFlag(t, source, flag.Key(), fmt.Sprintf("%d", i%10))
		}
		// Set to invalid
		setFlag(t, source, flag.Key(), "bad")
	})

	wg.Wait()
}
