package storage

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestClampUserDelegationTTL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		ttl  time.Duration
		want time.Duration
	}{
		{
			name: "short ttl is unchanged",
			ttl:  time.Hour,
			want: time.Hour,
		},
		{
			name: "exactly the cap is unchanged",
			ttl:  azureMaxUserDelegationTTL,
			want: azureMaxUserDelegationTTL,
		},
		{
			name: "over the cap is clamped to 7 days",
			ttl:  30 * 24 * time.Hour,
			want: azureMaxUserDelegationTTL,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, clampUserDelegationTTL(tt.ttl))
		})
	}
}

func TestAzureMetadataRoundTrip(t *testing.T) {
	t.Parallel()

	metadata := ObjectMetadata{
		ObjectMetadataTeamID:      "team-1",
		ObjectMetadataTemplateID:  "template-1",
		ObjectMetadataBuildOrigin: string(ObjectOriginPause),
		ObjectMetadataSoftDeleted: "reason:action-1",
		ObjectMetadataLogicalSize: "1024",
	}

	encoded := encodeAzureMetadata(metadata)

	// Hyphenated keys must be encoded to valid C# identifiers.
	for key := range encoded {
		assert.NotContains(t, key, "-")
	}

	assert.Equal(t, metadata, decodeAzureMetadata(encoded))
}

func TestAzureMetadataDecodeIsCaseInsensitive(t *testing.T) {
	t.Parallel()

	// Azure returns metadata keys case-normalized (e.g. first letter upper).
	value := "team-1"
	decoded := decodeAzureMetadata(map[string]*string{"Team_id": &value})

	assert.Equal(t, ObjectMetadata{ObjectMetadataTeamID: value}, decoded)
}

func TestAzureMetadataEmpty(t *testing.T) {
	t.Parallel()

	assert.Nil(t, encodeAzureMetadata(nil))
	assert.Nil(t, decodeAzureMetadata(nil))
}

func TestParseAzureConnectionString(t *testing.T) {
	t.Parallel()

	// AccountKey is base64 and may itself contain '='.
	accountName, accountKey := parseAzureConnectionString(
		"DefaultEndpointsProtocol=https;AccountName=myaccount;AccountKey=c2VjcmV0a2V5==;EndpointSuffix=core.windows.net",
	)

	assert.Equal(t, "myaccount", accountName)
	assert.Equal(t, "c2VjcmV0a2V5==", accountKey)
}
