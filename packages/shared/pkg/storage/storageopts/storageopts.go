// Package storageopts holds option types for the storage package, kept
// separate so generated mocks can reference them without an import cycle.
package storageopts

import "maps"

type ObjectMetadata map[string]string

const ObjectMetadataTeamID = "team_id"

// PutOptions holds parameters for blob/seekable writes. Compression is held
// as `any` so that storage.CompressConfig (which has heavy storage-internal
// dependencies) doesn't have to be moved here. Backends type-assert it back.
type PutOptions struct {
	Metadata    ObjectMetadata
	Compression any
}

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
