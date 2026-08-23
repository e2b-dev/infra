package storage

import (
	"encoding/base64"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
)

func TestAzureMetadataRoundTrip(t *testing.T) {
	t.Parallel()

	// Every metadata key the storage index defines, not a sample: the encoding is
	// key-shape-dependent (hyphens become double underscores), so a key that was never
	// round-tripped here is a key whose encoding is unverified.
	metadata := ObjectMetadata{
		ObjectMetadataTeamID:           "team-1",
		ObjectMetadataTemplateID:       "template-1",
		ObjectMetadataBuildOrigin:      string(ObjectOriginPause),
		ObjectMetadataSoftDeleted:      "reason:action-1",
		ObjectMetadataLogicalSize:      "1024",
		ObjectMetadataUncompressedSize: "2048",
		ObjectMetadataMappedSize:       "4096",
		ObjectMetadataDiffSize:         "512",
	}

	encoded, err := encodeAzureMetadata(metadata)
	require.NoError(t, err)

	// Hyphenated keys must be encoded to valid C# identifiers.
	for key := range encoded {
		assert.NotContains(t, key, "-")
	}

	assert.Equal(t, metadata, decodeAzureMetadata(encoded))
}

func TestAzureMetadataUnsafeKeyIsRejected(t *testing.T) {
	t.Parallel()

	// Unsafe keys used to be logged and dropped, completing the write with the metadata
	// silently missing. Refusing is the only behaviour a reader can reason about.
	for _, key := range []string{"a__b", "a_-b", "a-_b", "TeamID"} {
		t.Run(key, func(t *testing.T) {
			t.Parallel()

			_, err := encodeAzureMetadata(ObjectMetadata{key: "v"})
			require.Error(t, err)
		})
	}
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

	encoded, err := encodeAzureMetadata(nil)
	require.NoError(t, err)
	assert.Nil(t, encoded)
	assert.Nil(t, decodeAzureMetadata(nil))
}

func TestAzureMetadataKeyUnsafe(t *testing.T) {
	t.Parallel()

	// Every key that survives encode+decode unchanged must be accepted, and
	// every key whose encoding is ambiguous must be rejected: "a_-b" and "a-_b"
	// both encode to "a___b", so neither can round-trip; an uppercase key is
	// case-normalized by Azure and lowercased on decode, so it reads back as a
	// different key.
	safe := []string{
		ObjectMetadataTeamID, ObjectMetadataTemplateID, ObjectMetadataBuildOrigin,
		ObjectMetadataSoftDeleted, ObjectMetadataLogicalSize,
		"plain", "with-hyphen", "with_underscore", "a-b_c",
	}
	for _, key := range safe {
		assert.False(t, azureMetadataKeyUnsafe(key), "key %q must be storable", key)

		encoded, err := encodeAzureMetadata(ObjectMetadata{key: "v"})
		require.NoError(t, err)
		assert.Equal(t, ObjectMetadata{key: "v"}, decodeAzureMetadata(encoded), "key %q must round-trip", key)
	}

	for _, key := range []string{"a__b", "a_-b", "a-_b", "TeamID", "Upper-Case"} {
		assert.True(t, azureMetadataKeyUnsafe(key), "key %q must be rejected", key)

		_, err := encodeAzureMetadata(ObjectMetadata{key: "v"})
		require.Error(t, err, "key %q must fail the write, not be skipped", key)
	}
}

func TestAzurePartUploaderBlockIDsAreUploadScoped(t *testing.T) {
	t.Parallel()

	// Azure stages blocks in a per-blob namespace with no upload identity of its own, so
	// two uploaders writing the same path must not generate colliding block IDs -- one
	// commit could otherwise mix both bodies. Assert the ID itself, not just the nonce
	// field: dropping uploadID from blockID is exactly the regression that matters, and a
	// test that only compares uploadIDs stays green through it.
	a, err := newAzurePartUploader(nil, nil)
	require.NoError(t, err)
	b, err := newAzurePartUploader(nil, nil)
	require.NoError(t, err)

	require.NotEqual(t, a.uploadID, b.uploadID)
	require.Len(t, a.uploadID, 16)

	t.Run("same part index, different uploaders, different block IDs", func(t *testing.T) {
		t.Parallel()

		assert.NotEqual(t, a.blockID(1), b.blockID(1))
	})

	t.Run("block ID carries the uploader's nonce", func(t *testing.T) {
		t.Parallel()

		decoded, err := base64.StdEncoding.DecodeString(a.blockID(7))
		require.NoError(t, err)
		assert.Contains(t, string(decoded), a.uploadID)
		assert.NotContains(t, string(decoded), b.uploadID)
	})

	t.Run("part index still distinguishes blocks within one upload", func(t *testing.T) {
		t.Parallel()

		assert.NotEqual(t, a.blockID(1), a.blockID(2))
	})

	t.Run("all block IDs for a blob decode to the same length", func(t *testing.T) {
		t.Parallel()

		// Azure rejects a commit whose block IDs are not equal-length.
		first, err := base64.StdEncoding.DecodeString(a.blockID(1))
		require.NoError(t, err)

		for _, part := range []int{2, 99, 12345678} {
			other, err := base64.StdEncoding.DecodeString(a.blockID(part))
			require.NoError(t, err)
			assert.Len(t, other, len(first), "part %d changed the block-ID length", part)
		}
	})
}

func TestNewAzureStorageAcceptsKeylessConnectionString(t *testing.T) {
	// A least-privilege SAS connection string (no AccountKey) is a working config for
	// every reachable operation — nothing in the provider needs a shared key. (Signed
	// upload URLs would have, but UploadSignedURL is hard-disabled on Azure; the AAD
	// path is likewise accepted without one.)
	t.Setenv("AZURE_STORAGE_CONNECTION_STRING",
		"BlobEndpoint=https://myaccount.blob.core.windows.net;SharedAccessSignature=sv=2022-11-02&ss=b&sig=fake")

	s, err := newAzureStorage(t.Context(), "fc-templates", nil)
	require.NoError(t, err)
	require.NotNil(t, s)
}

func TestNewAzureStorageAcceptsConnectionStringWithAccountKey(t *testing.T) {
	// The account-key shape stays accepted alongside the SAS one above.
	t.Setenv("AZURE_STORAGE_CONNECTION_STRING",
		"DefaultEndpointsProtocol=https;AccountName=myaccount;AccountKey=ZmFrZS1hY2NvdW50LWtleS1mb3ItdGVzdHMtb25seS1ub3QtYS1jcmVkZW50aWFs;EndpointSuffix=core.windows.net")

	s, err := newAzureStorage(t.Context(), "fc-templates", nil)
	require.NoError(t, err)
	require.NotNil(t, s)
}

func TestAzureUploadConcurrencyComesFromTheLimiter(t *testing.T) {
	t.Parallel()

	// A nil limiter reports the feature flag's fallback, which is deliberately not the
	// same as the 8 the provider once hardcoded: if the wiring is reverted to a constant,
	// this assertion fails. A host tuned down to bound memory must not still stage eight
	// 10 MB blocks per upload.
	o := &azureObject{limiter: nil}
	opts := o.uploadFileOptions(t.Context(), nil)

	require.NotNil(t, opts)
	assert.Equal(t, uint16(featureflags.StorageMaxUploadTasks.Fallback()), opts.Concurrency)
	assert.NotEqual(t, uint16(8), opts.Concurrency,
		"the fallback must differ from the old constant, or this test proves nothing")
	assert.Equal(t, int64(azureUploadBlockSize), opts.BlockSize)
}

func TestClampAzureUploadConcurrency(t *testing.T) {
	t.Parallel()

	// The limiter value is an operator-settable int; the SDK field is a uint16.
	// A negative throttle must degrade to the SDK default (0), not wrap to
	// 65535-way concurrency on the very host being tuned down.
	tests := []struct {
		name  string
		tasks int
		want  uint16
	}{
		{name: "negative degrades to SDK default", tasks: -1, want: 0},
		{name: "zero passes through", tasks: 0, want: 0},
		{name: "in-range passes through", tasks: 16, want: 16},
		{name: "uint16 max passes through", tasks: math.MaxUint16, want: math.MaxUint16},
		{name: "over uint16 max is clamped", tasks: math.MaxUint16 + 1, want: math.MaxUint16},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, clampAzureUploadConcurrency(tt.tasks))
		})
	}
}
