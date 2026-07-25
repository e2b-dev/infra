package analyticscollector

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewAnalyticsTarget(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		host       string
		useTLS     bool
		wantTarget string
	}{
		{
			name:       "bare hostname defaults to the HTTPS port",
			host:       "collector.example.com",
			useTLS:     true,
			wantTarget: "collector.example.com:443",
		},
		{
			name:       "explicit port is kept",
			host:       "collector.example.com:8443",
			useTLS:     true,
			wantTarget: "collector.example.com:8443",
		},
		{
			// The local Belt collector: plaintext gRPC on loopback. Joining
			// :443 onto this used to produce "[localhost:5051]:443", which the
			// resolver could not resolve.
			name:       "plaintext local collector",
			host:       "localhost:5051",
			useTLS:     false,
			wantTarget: "localhost:5051",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			analytics, err := NewAnalytics(tt.host, "api-token", tt.useTLS)
			require.NoError(t, err)

			t.Cleanup(func() { _ = analytics.Close() })

			require.NotNil(t, analytics.connection)
			assert.Equal(t, tt.wantTarget, analytics.connection.Target())
		})
	}
}

func TestNewAnalyticsWithoutHostIsNoop(t *testing.T) {
	t.Parallel()

	analytics, err := NewAnalytics("", "", true)
	require.NoError(t, err)

	assert.Nil(t, analytics.client)
	assert.Nil(t, analytics.connection)

	res, err := analytics.InstanceStarted(t.Context(), &InstanceStartedEvent{})
	assert.NoError(t, err)
	assert.Nil(t, res)

	assert.NoError(t, analytics.Close())
}
