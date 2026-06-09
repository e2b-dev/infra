package paths

import (
	"testing"

	"github.com/stretchr/testify/require"
)

const validHash = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestGetLayerFilesCachePath(t *testing.T) {
	t.Parallel()

	p, err := GetLayerFilesCachePath("team-id", validHash)
	require.NoError(t, err)
	require.Equal(t, "team-id/files/"+validHash+".tar", p)
}

func TestGetLayerFilesCachePathRejectsInvalidHashes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		cacheScope string
		hash       string
	}{
		{
			name:       "hash with slash",
			cacheScope: "team-id",
			hash:       "../other-team/files/hash",
		},
		{
			name:       "hash with backslash",
			cacheScope: "team-id",
			hash:       `..\other-team`,
		},
		{
			name:       "dot hash",
			cacheScope: "team-id",
			hash:       ".",
		},
		{
			name:       "dot dot hash",
			cacheScope: "team-id",
			hash:       "..",
		},
		{
			name:       "not a hash",
			cacheScope: "team-id",
			hash:       "Dockerfile",
		},
		{
			name:       "short hash",
			cacheScope: "team-id",
			hash:       "abcdef123456",
		},
		{
			name:       "uppercase hash",
			cacheScope: "team-id",
			hash:       "0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF",
		},
		{
			name:       "non hex hash",
			cacheScope: "team-id",
			hash:       "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdeg",
		},
		{
			name:       "cache scope with slash",
			cacheScope: "team/id",
			hash:       validHash,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := GetLayerFilesCachePath(tt.cacheScope, tt.hash)
			require.Error(t, err)
		})
	}
}

func TestHashToPath(t *testing.T) {
	t.Parallel()

	p, err := HashToPath("team-id", validHash)
	require.NoError(t, err)
	require.Equal(t, "team-id/index/"+validHash, p)
}

func TestHashToPathRejectsInvalidHash(t *testing.T) {
	t.Parallel()

	_, err := HashToPath("team-id", "../metadata")
	require.Error(t, err)
}
