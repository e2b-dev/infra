package storage

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// isolateAWSEnv makes AWS SDK config loading hermetic: no host profiles or
// shared config files leak into the test.
func isolateAWSEnv(t *testing.T) {
	t.Helper()

	dir := t.TempDir()
	t.Setenv("AWS_PROFILE", "")
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(dir, "config"))
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(dir, "credentials"))
}

func TestNewProvider_Local(t *testing.T) {
	t.Parallel()

	base := t.TempDir()

	provider, err := NewProvider(t.Context(), Spec{Provider: LocalStorageProvider, BasePath: base})
	require.NoError(t, err)

	fs, ok := provider.(*fsStorage)
	require.True(t, ok, "expected *fsStorage, got %T", provider)
	assert.Equal(t, base, fs.basePath)
}

func TestNewProvider_LocalWithUpload(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	hmacKey := []byte("0123456789abcdef0123456789abcdef")

	provider, err := NewProvider(t.Context(),
		Spec{Provider: LocalStorageProvider, BasePath: base},
		WithLocalUpload("http://localhost:5008", hmacKey))
	require.NoError(t, err)

	fs, ok := provider.(*fsStorage)
	require.True(t, ok, "expected *fsStorage, got %T", provider)
	assert.Equal(t, "http://localhost:5008", fs.uploadURL)
	assert.Equal(t, hmacKey, fs.hmacKey)
}

//nolint:paralleltest // isolateAWSEnv uses t.Setenv
func TestNewProvider_AWS(t *testing.T) {
	isolateAWSEnv(t)

	provider, err := NewProvider(t.Context(), Spec{
		Provider:     AWSStorageProvider,
		Bucket:       "some-bucket",
		Endpoint:     "http://minio:9000",
		UsePathStyle: true,
	})
	require.NoError(t, err)

	s3p, ok := provider.(*awsStorage)
	require.True(t, ok, "expected *awsStorage, got %T", provider)
	assert.Equal(t, "some-bucket", s3p.bucketName)
}

func TestNewProvider_UnknownProvider(t *testing.T) {
	t.Parallel()

	_, err := NewProvider(t.Context(), Spec{Provider: "FloppyDisk"})
	require.ErrorContains(t, err, "unknown storage provider: FloppyDisk")
}
