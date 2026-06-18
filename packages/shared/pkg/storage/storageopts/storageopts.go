// Package storageopts holds option types for the storage package, kept
// separate so generated mocks can reference them without an import cycle.
package storageopts

import (
	"context"
	"maps"
)

type ObjectMetadata map[string]string

const ObjectMetadataTeamID = "team_id"

// ObjectMetadataSoftDeleted is a mutable tombstone written by the storage index
// (not at upload time) to mark a layer for deletion. Value is
// "<reason>:<action_id>". Consumers fail closed on it behind a feature flag.
const ObjectMetadataSoftDeleted = "storage-index-soft-deleted"

// FrameSink fires once per compressed frame with its absolute C-space offset.
// Best-effort; implementations should return quickly and bound their own I/O.
type FrameSink func(ctx context.Context, cOffset int64, compressed []byte)

// PutOptions holds parameters for blob/seekable writes. Compression is held
// as `any` so that storage.CompressConfig (which has heavy storage-internal
// dependencies) doesn't have to be moved here. Backends type-assert it back.
type PutOptions struct {
	Metadata    ObjectMetadata
	Compression any
	FrameSink   FrameSink
	Checksum    bool
}

func WithFrameSink(s FrameSink) PutOption { return func(o *PutOptions) { o.FrameSink = s } }

type PutOption func(*PutOptions)

func WithMetadata(metadata ObjectMetadata) PutOption {
	return func(o *PutOptions) {
		if len(metadata) == 0 {
			return
		}
		if o.Metadata == nil {
			o.Metadata = make(ObjectMetadata, len(metadata))
		}
		maps.Copy(o.Metadata, metadata)
	}
}

// WithCompression stashes a compression config (typed in the storage package)
// into PutOptions. The storage package wraps this with a typed helper.
func WithCompression(cfg any) PutOption {
	return func(o *PutOptions) { o.Compression = cfg }
}

func Apply(opts []PutOption) PutOptions {
	var p PutOptions
	for _, opt := range opts {
		opt(&p)
	}

	return p
}
