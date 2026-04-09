package teamprovision

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNewProvisionSink_DisabledReturnsNoop(t *testing.T) {
	t.Parallel()

	sink, err := NewProvisionSink(false, "", "", 15*time.Second)
	require.NoError(t, err)
	require.IsType(t, &NoopProvisionSink{}, sink)
}

func TestNewProvisionSink_EnabledRequiresBaseURL(t *testing.T) {
	t.Parallel()

	sink, err := NewProvisionSink(true, "", "token", 15*time.Second)
	require.Nil(t, sink)
	require.ErrorIs(t, err, ErrMissingBaseURL)
}

func TestNewProvisionSink_EnabledRequiresAPIToken(t *testing.T) {
	t.Parallel()

	sink, err := NewProvisionSink(true, "https://billing.example.com", "", 15*time.Second)
	require.Nil(t, sink)
	require.ErrorIs(t, err, ErrMissingAPIToken)
}

func TestNewProvisionSink_EnabledReturnsHTTPSink(t *testing.T) {
	t.Parallel()

	sink, err := NewProvisionSink(true, "https://billing.example.com", "token", 15*time.Second)
	require.NoError(t, err)
	require.IsType(t, &HTTPProvisionSink{}, sink)
}
