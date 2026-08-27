package pool

import (
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

func testDestination(connectionKey string, insecureSkipTLSVerify bool) *Destination {
	return &Destination{
		Url:                   &url.URL{Scheme: "https", Host: "127.0.0.1:8443"},
		SandboxId:             "test-sandbox",
		ConnectionKey:         connectionKey,
		RequestLogger:         logger.NewNopLogger(),
		InsecureSkipTLSVerify: insecureSkipTLSVerify,
	}
}

// Reuse is the expected outcome, not a bug: a key already in the pool keeps the
// TLS policy of the destination that built it.
func TestPoolReusesClientAcrossInsecureSkipTLSVerify(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name  string
		first bool
	}{
		{name: "first destination skips verification", first: true},
		{name: "first destination verifies", first: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			p := New(4, 1, time.Second, false)

			client := p.Get(t.Context(), testDestination("lifecycle", tt.first))
			require.NotNil(t, client)

			assert.Same(t, client, p.Get(t.Context(), testDestination("lifecycle", !tt.first)))
			assert.Equal(t, tt.first, client.transport.TLSClientConfig != nil)
		})
	}
}

func TestPoolBuildsOneClientPerConnectionKey(t *testing.T) {
	t.Parallel()

	p := New(4, 1, time.Second, false)

	first := p.Get(t.Context(), testDestination("lifecycle-a", true))
	second := p.Get(t.Context(), testDestination("lifecycle-b", false))

	assert.NotSame(t, first, second)
	assert.Equal(t, 2, p.Size())
	assert.NotNil(t, first.transport.TLSClientConfig)
	assert.Nil(t, second.transport.TLSClientConfig)
}
