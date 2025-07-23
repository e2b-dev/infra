package storage

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCachedFileObjectProvider_MakeChunkFilename(t *testing.T) {
	c := CachedFileObjectProvider{path: "/a/b/c", chunkSize: 1024}
	filename := c.makeChunkFilename(4192)
	assert.Equal(t, "/a/b/c/000000004192-1024.bin", filename)
}
