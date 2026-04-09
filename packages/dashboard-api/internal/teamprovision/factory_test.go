package teamprovision

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewProvisionSink_DisabledReturnsNoop(t *testing.T) {
	t.Parallel()

	sink, err := NewProvisionSink(false, "", "")
	require.NoError(t, err)
	require.IsType(t, &NoopProvisionSink{}, sink)
}

func TestNewProvisionSink_EnabledRequiresBaseURL(t *testing.T) {
	t.Parallel()

	sink, err := NewProvisionSink(true, "", "token")
	require.Nil(t, sink)
	require.ErrorIs(t, err, ErrMissingBaseURL)
}

func TestNewProvisionSink_EnabledRequiresAPIToken(t *testing.T) {
	t.Parallel()

	sink, err := NewProvisionSink(true, "https://billing.example.com", "")
	require.Nil(t, sink)
	require.ErrorIs(t, err, ErrMissingAPIToken)
}

func TestNewProvisionSink_EnabledReturnsHTTPSink(t *testing.T) {
	t.Parallel()

	sink, err := NewProvisionSink(true, "https://billing.example.com", "token")
	require.NoError(t, err)
	require.IsType(t, &HTTPProvisionSink{}, sink)
}
