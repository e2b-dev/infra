package handlers

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsTemplateLayerFilesHash(t *testing.T) {
	t.Parallel()

	validHash := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	require.True(t, isTemplateLayerFilesHash(validHash))

	tests := []string{
		"../tenant-b/files/hash",
		`..\team-id`,
		".",
		"..",
		"Dockerfile",
		"abcdef123456",
		"0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF",
		"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdeg",
	}

	for _, tt := range tests {
		t.Run(tt, func(t *testing.T) {
			t.Parallel()

			require.False(t, isTemplateLayerFilesHash(tt))
		})
	}
}
