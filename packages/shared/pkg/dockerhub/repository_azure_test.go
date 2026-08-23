package dockerhub

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewAzureRemoteRepositoryValidatesURL(t *testing.T) {
	t.Parallel()

	// A malformed repository URL must fail at construction with a config
	// error, not at the first pull as an opaque token-exchange failure
	// against a host like "https:".
	for _, tt := range []struct {
		name    string
		url     string
		wantErr string
	}{
		{name: "scheme prefix rejected", url: "https://myregistry.azurecr.io/dockerhub", wantErr: "invalid Azure remote repository URL"},
		{name: "empty rejected", url: "", wantErr: "invalid Azure remote repository URL"},
		{name: "registry-less path rejected", url: "dockerhub", wantErr: "invalid Azure remote repository URL"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := NewAzureRemoteRepository(t.Context(), tt.url)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestNewAzureRemoteRepositoryAcceptsRegistryHostURL(t *testing.T) {
	t.Parallel()

	// The complementary case, so the validation cannot pass by rejecting
	// everything.
	r, err := NewAzureRemoteRepository(t.Context(), "myregistry.azurecr.io/dockerhub")
	require.NoError(t, err)
	require.NotNil(t, r)
}
