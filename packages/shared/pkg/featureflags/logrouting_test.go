package featureflags

import (
	"testing"
	"time"

	"github.com/launchdarkly/go-sdk-common/v3/ldvalue"
	"github.com/launchdarkly/go-server-sdk/v7/testhelpers/ldtestdata"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const fallbackCollector = "http://localhost:30006"

func legacyLogWriteConfig() LogWriteConfig {
	// The legacy fallback leaves Timeout at 0 (rely on the HTTP client timeout),
	// preserving pre-flag behavior. defaultLogWriteTimeout applies only to
	// explicitly-configured flags with a missing/invalid timeout_ms.
	return LogWriteConfig{
		PrimaryURL:              fallbackCollector,
		Timeout:                 0,
		MaxInflightShadowWrites: defaultMaxInflightShadowWrites,
	}
}

func TestResolveLogWriteConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value ldvalue.Value
		want  LogWriteConfig
	}{
		{
			name:  "null falls back to legacy collector",
			value: ldvalue.Null(),
			want:  legacyLogWriteConfig(),
		},
		{
			name:  "non-object falls back to legacy collector",
			value: ldvalue.String("nonsense"),
			want:  legacyLogWriteConfig(),
		},
		{
			name: "unknown mode falls back to legacy collector",
			value: ldvalue.FromJSONMarshal(map[string]any{
				"mode":        "bogus",
				"primary_url": "http://localhost:9999",
			}),
			want: legacyLogWriteConfig(),
		},
		{
			name: "non-string mode falls back to legacy collector",
			value: ldvalue.FromJSONMarshal(map[string]any{
				"mode":        true,
				"primary_url": "http://localhost:9999",
			}),
			want: legacyLogWriteConfig(),
		},
		{
			name: "missing mode falls back to legacy collector",
			value: ldvalue.FromJSONMarshal(map[string]any{
				"primary_url": "http://localhost:9999",
			}),
			want: legacyLogWriteConfig(),
		},
		{
			name: "primary_only with valid local url",
			value: ldvalue.FromJSONMarshal(map[string]any{
				"mode":        LogsWriteModePrimaryOnly,
				"primary_url": "http://127.0.0.1:30006",
				"timeout_ms":  1500,
			}),
			want: LogWriteConfig{PrimaryURL: "http://127.0.0.1:30006", Timeout: 1500 * time.Millisecond, MaxInflightShadowWrites: defaultMaxInflightShadowWrites},
		},
		{
			name: "primary_only trims url whitespace",
			value: ldvalue.FromJSONMarshal(map[string]any{
				"mode":        LogsWriteModePrimaryOnly,
				"primary_url": "  http://127.0.0.1:30006/logs  ",
			}),
			want: LogWriteConfig{PrimaryURL: "http://127.0.0.1:30006/logs", Timeout: defaultLogWriteTimeout, MaxInflightShadowWrites: defaultMaxInflightShadowWrites},
		},
		{
			name: "primary_only non-string primary url falls back",
			value: ldvalue.FromJSONMarshal(map[string]any{
				"mode":        LogsWriteModePrimaryOnly,
				"primary_url": 123,
			}),
			want: legacyLogWriteConfig(),
		},
		{
			name: "primary_only empty primary url falls back",
			value: ldvalue.FromJSONMarshal(map[string]any{
				"mode":        LogsWriteModePrimaryOnly,
				"primary_url": "",
			}),
			want: legacyLogWriteConfig(),
		},
		{
			name: "primary_only external https url falls back (unsafe)",
			value: ldvalue.FromJSONMarshal(map[string]any{
				"mode":        LogsWriteModePrimaryOnly,
				"primary_url": "https://evil.example.com/logs",
			}),
			want: legacyLogWriteConfig(),
		},
		{
			name: "primary_only external http host falls back (unsafe)",
			value: ldvalue.FromJSONMarshal(map[string]any{
				"mode":        LogsWriteModePrimaryOnly,
				"primary_url": "http://evil.example.com/logs",
			}),
			want: legacyLogWriteConfig(),
		},
		{
			name: "primary_and_shadow with valid urls",
			value: ldvalue.FromJSONMarshal(map[string]any{
				"mode":        LogsWriteModePrimaryAndShadow,
				"primary_url": "http://localhost:30006",
				"shadow_urls": []any{"http://127.0.0.1:4321/logs", "http://[::1]:4321/logs"},
				"timeout_ms":  2500,
			}),
			want: LogWriteConfig{
				PrimaryURL:              "http://localhost:30006",
				ShadowURLs:              []string{"http://127.0.0.1:4321/logs", "http://[::1]:4321/logs"},
				Timeout:                 2500 * time.Millisecond,
				MaxInflightShadowWrites: defaultMaxInflightShadowWrites,
			},
		},
		{
			name: "primary_and_shadow deduplicates primary and duplicate shadows",
			value: ldvalue.FromJSONMarshal(map[string]any{
				"mode":        LogsWriteModePrimaryAndShadow,
				"primary_url": "http://localhost:30006",
				"shadow_urls": []any{"http://localhost:30006", "http://127.0.0.1:4321/logs", "http://127.0.0.1:4321/logs"},
			}),
			want: LogWriteConfig{
				PrimaryURL:              "http://localhost:30006",
				ShadowURLs:              []string{"http://127.0.0.1:4321/logs"},
				Timeout:                 defaultLogWriteTimeout,
				MaxInflightShadowWrites: defaultMaxInflightShadowWrites,
			},
		},
		{
			name: "primary_and_shadow non-array shadow urls falls back",
			value: ldvalue.FromJSONMarshal(map[string]any{
				"mode":        LogsWriteModePrimaryAndShadow,
				"primary_url": "http://localhost:30006",
				"shadow_urls": "http://127.0.0.1:4321/logs",
			}),
			want: legacyLogWriteConfig(),
		},
		{
			name: "primary_and_shadow non-string shadow falls back",
			value: ldvalue.FromJSONMarshal(map[string]any{
				"mode":        LogsWriteModePrimaryAndShadow,
				"primary_url": "http://localhost:30006",
				"shadow_urls": []any{"http://127.0.0.1:4321/logs", 42},
			}),
			want: legacyLogWriteConfig(),
		},
		{
			name: "primary_and_shadow too many shadows falls back",
			value: ldvalue.FromJSONMarshal(map[string]any{
				"mode":        LogsWriteModePrimaryAndShadow,
				"primary_url": "http://localhost:30006",
				"shadow_urls": []any{
					"http://127.0.0.1:4321/a",
					"http://127.0.0.1:4321/b",
					"http://127.0.0.1:4321/c",
					"http://127.0.0.1:4321/d",
					"http://127.0.0.1:4321/e",
				},
			}),
			want: legacyLogWriteConfig(),
		},
		{
			name: "primary_and_shadow with unsafe shadow falls back",
			value: ldvalue.FromJSONMarshal(map[string]any{
				"mode":        LogsWriteModePrimaryAndShadow,
				"primary_url": "http://localhost:30006",
				"shadow_urls": []any{"http://8.8.8.8/logs"},
			}),
			want: legacyLogWriteConfig(),
		},
		{
			name: "timeout defaulted when non-positive",
			value: ldvalue.FromJSONMarshal(map[string]any{
				"mode":        LogsWriteModePrimaryOnly,
				"primary_url": "http://localhost:30006",
				"timeout_ms":  0,
			}),
			want: LogWriteConfig{PrimaryURL: "http://localhost:30006", Timeout: defaultLogWriteTimeout, MaxInflightShadowWrites: defaultMaxInflightShadowWrites},
		},
		{
			name: "timeout capped when too large",
			value: ldvalue.FromJSONMarshal(map[string]any{
				"mode":        LogsWriteModePrimaryOnly,
				"primary_url": "http://localhost:30006",
				"timeout_ms":  60000,
			}),
			want: LogWriteConfig{PrimaryURL: "http://localhost:30006", Timeout: maxLogWriteTimeout, MaxInflightShadowWrites: defaultMaxInflightShadowWrites},
		},
		{
			name: "max inflight shadow writes configured",
			value: ldvalue.FromJSONMarshal(map[string]any{
				"mode":                       LogsWriteModePrimaryOnly,
				"primary_url":                "http://localhost:30006",
				"max_inflight_shadow_writes": 64,
			}),
			want: LogWriteConfig{PrimaryURL: "http://localhost:30006", Timeout: defaultLogWriteTimeout, MaxInflightShadowWrites: 64},
		},
		{
			name: "max inflight shadow writes defaults when non-positive",
			value: ldvalue.FromJSONMarshal(map[string]any{
				"mode":                       LogsWriteModePrimaryOnly,
				"primary_url":                "http://localhost:30006",
				"max_inflight_shadow_writes": 0,
			}),
			want: LogWriteConfig{PrimaryURL: "http://localhost:30006", Timeout: defaultLogWriteTimeout, MaxInflightShadowWrites: defaultMaxInflightShadowWrites},
		},
		{
			name: "max inflight shadow writes uses positive value as configured",
			value: ldvalue.FromJSONMarshal(map[string]any{
				"mode":                       LogsWriteModePrimaryOnly,
				"primary_url":                "http://localhost:30006",
				"max_inflight_shadow_writes": 999999,
			}),
			want: LogWriteConfig{PrimaryURL: "http://localhost:30006", Timeout: defaultLogWriteTimeout, MaxInflightShadowWrites: 999999},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Each subtest gets its own datasource/client so parallel runs don't
			// race on the shared flag value.
			source := ldtestdata.DataSource()
			client, err := NewClientWithDatasource(source)
			require.NoError(t, err)
			t.Cleanup(func() {
				assert.NoError(t, client.Close(t.Context()))
			})

			source.Update(source.Flag(LogsWriteConfigFlag.Key()).ValueForAll(tt.value))

			got := ResolveLogWriteConfig(t.Context(), client, fallbackCollector)

			assert.Equal(t, tt.want.PrimaryURL, got.PrimaryURL)
			assert.Equal(t, tt.want.ShadowURLs, got.ShadowURLs)
			assert.Equal(t, tt.want.Timeout, got.Timeout)
			assert.Equal(t, tt.want.MaxInflightShadowWrites, got.MaxInflightShadowWrites)
		})
	}
}

func TestResolveLogWriteConfigNilClientFallsBack(t *testing.T) {
	t.Parallel()

	got := ResolveLogWriteConfig(t.Context(), nil, fallbackCollector)

	assert.Equal(t, fallbackCollector, got.PrimaryURL)
	assert.Nil(t, got.ShadowURLs)
	// Legacy fallback leaves Timeout at 0 to rely on the HTTP client timeout.
	assert.Equal(t, time.Duration(0), got.Timeout)
	assert.Equal(t, int64(defaultMaxInflightShadowWrites), got.MaxInflightShadowWrites)
}

func TestIsSafeLogURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		url  string
		want bool
	}{
		{"", false},
		{"http://localhost:30006", true},
		{"http://127.0.0.1:30006", true},
		{"http://[::1]:4321/logs", true},
		{"http://10.0.0.5:9000/logs", true},
		{"http://192.168.1.10/logs", true},
		{"http://169.254.0.1/logs", true},
		{"http://otel-router.service.consul:4321/logs", true},
		{"http://vector.logging.svc.cluster.local:8080/logs", true},
		{"http://collector.logging.svc:8080/logs", true},
		{"http://logs.internal:8080/logs", true},
		{"http://logs.local:8080/logs", true},
		{"https://localhost:30006", false},
		{"http://8.8.8.8/logs", false},
		{"http://example.com/logs", false},
		{"://bad", false},
		{"ftp://localhost/logs", false},
	}

	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, isSafeLogURL(tt.url))
		})
	}
}
