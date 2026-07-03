package storage

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// The empty-prefix guard must reject before touching the S3 client, so a nil
// client is enough to exercise it.
func TestAWSDeleteObjectsWithPrefixRejectsEmptyPrefix(t *testing.T) {
	t.Parallel()

	s := &awsStorage{bucketName: "test-bucket"}

	err := s.DeleteObjectsWithPrefix(context.Background(), "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "empty prefix")
}
