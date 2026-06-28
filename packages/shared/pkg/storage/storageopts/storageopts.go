// Package storageopts holds option types for the storage package, kept
// separate so generated mocks can reference them without an import cycle.
package storageopts

import (
	"context"
	"maps"
	"regexp"
	"strings"
)

type ObjectMetadata map[string]string

// Custom object metadata keys for the storage index (immutable, set-once).
const (
	ObjectMetadataTeamID      = "team_id"
	ObjectMetadataTemplateID  = "template_id"
	ObjectMetadataBuildOrigin = "build_origin"
)

// ObjectOrigin is the immutable operation that created a build, stored as the
// ObjectMetadataBuildOrigin value.
type ObjectOrigin string

const (
	ObjectOriginPause              ObjectOrigin = "pause"
	ObjectOriginTemplateBuild      ObjectOrigin = "template_build"
	ObjectOriginTemplateBuildCache ObjectOrigin = "template_build_cache"
	ObjectOriginSnapshotTemplate   ObjectOrigin = "snapshot_template"
)

// ObjectMetadataSoftDeleted is a mutable tombstone written by the storage index
// (not at upload time) to mark a layer for deletion. Value is
// "<reason>:<action_id>". Consumers fail closed on it behind a feature flag.
const ObjectMetadataSoftDeleted = "storage-index-soft-deleted"

// SoftDeleteReasonOther is the fallback group used when a tombstone reason is
// empty or not a simple low-cardinality token.
const SoftDeleteReasonOther = "other"

// softDeleteReasonPattern bounds what is emitted as a metric dimension so a
// malformed or high-cardinality reason cannot explode metric series.
var softDeleteReasonPattern = regexp.MustCompile(`^[a-z0-9_-]{1,32}$`)

// ParseSoftDeleteMarker splits an ObjectMetadataSoftDeleted value of the form
// "<reason>:<action_id>" into its parts. A marker without a ":" is treated as a
// bare reason with an empty action ID; either part may be empty.
func ParseSoftDeleteMarker(marker string) (reason, actionID string) {
	reason, actionID, found := strings.Cut(marker, ":")
	if !found {
		return marker, ""
	}

	return reason, actionID
}

// SoftDeleteReasonGroup normalizes a parsed reason into a low-cardinality
// metric dimension, collapsing empty or unexpected values to SoftDeleteReasonOther.
func SoftDeleteReasonGroup(reason string) string {
	if softDeleteReasonPattern.MatchString(reason) {
		return reason
	}

	return SoftDeleteReasonOther
}

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
