package block

import (
	lru "github.com/hashicorp/golang-lru"
)

// DefaultLRUFrameCount is the default number of decompressed frames to keep in the LRU cache.
// With 64MB frames, 16 frames = 1GB max memory usage.
const DefaultLRUFrameCount = 16

// cachedFrame holds a decompressed frame and its metadata.
type cachedFrame struct {
	data   []byte // decompressed frame data (not copied, owned by the cache)
	offset int64  // uncompressed start offset of this frame
	size   int64  // uncompressed size of this frame
}

// FrameLRU provides an in-memory LRU cache for decompressed frames.
// Keys are frame start offsets (uncompressed), values are the decompressed frame data.
// Caching entire frames avoids re-decompression when adjacent pages are faulted in.
type FrameLRU struct {
	cache *lru.Cache
}

// NewFrameLRU creates a new FrameLRU with the specified maximum number of frames.
func NewFrameLRU(maxFrames int) (*FrameLRU, error) {
	cache, err := lru.New(maxFrames)
	if err != nil {
		return nil, err
	}

	return &FrameLRU{
		cache: cache,
	}, nil
}

// get retrieves a frame from the LRU cache by its start offset.
// Returns the cached frame and true if found, nil and false otherwise.
func (l *FrameLRU) get(frameOffset int64) (*cachedFrame, bool) {
	val, ok := l.cache.Get(frameOffset)
	if !ok {
		return nil, false
	}

	frame, ok := val.(*cachedFrame)
	if !ok {
		return nil, false
	}

	return frame, true
}

// put stores a decompressed frame in the LRU cache.
// The data slice is stored directly (not copied) - caller must not modify it after this call.
func (l *FrameLRU) put(frameOffset int64, frameSize int64, data []byte) {
	frame := &cachedFrame{
		data:   data,
		offset: frameOffset,
		size:   frameSize,
	}
	l.cache.Add(frameOffset, frame)
}

// Len returns the number of frames in the cache.
func (l *FrameLRU) Len() int {
	return l.cache.Len()
}

// Purge removes all frames from the cache.
func (l *FrameLRU) Purge() {
	l.cache.Purge()
}
