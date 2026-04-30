// Package storageopts holds option types for the storage package, kept in a
// separate package so generated mocks can reference them without an import
// cycle.
package storageopts

import "maps"

// ObjectMetadata is custom user metadata attached to objects on upload.
// Backends without custom metadata silently ignore it.
type ObjectMetadata map[string]string

const (
	ObjectMetadataTeamID        = "team_id"
	ObjectMetadataRootBuildID   = "root_build_id"
	ObjectMetadataParentBuildID = "parent_build_id"
	ObjectMetadataBuildKind     = "build_kind"
)

// Values for ObjectMetadataBuildKind.
const (
	BuildKindTemplateLayer     = "template_layer"
	BuildKindSandboxPause      = "sandbox_pause"
	BuildKindSandboxCheckpoint = "sandbox_checkpoint"
)

// SnapshotUploadMetadata splits labels by attachment scope: Common goes on
// every uploaded artifact, MetadataOnly goes only on metadata.json.
type SnapshotUploadMetadata struct {
	Common       ObjectMetadata
	MetadataOnly ObjectMetadata
}

// BuildLineageMetadata returns the per-build label map. parentBuildID is
// omitted when empty.
func BuildLineageMetadata(buildKind, parentBuildID string) ObjectMetadata {
	out := ObjectMetadata{
		ObjectMetadataBuildKind: buildKind,
	}
	if parentBuildID != "" {
		out[ObjectMetadataParentBuildID] = parentBuildID
	}

	return out
}

// MergedForMetadata returns Common ∪ MetadataOnly. MetadataOnly wins on collision.
func (m SnapshotUploadMetadata) MergedForMetadata() ObjectMetadata {
	if len(m.Common) == 0 && len(m.MetadataOnly) == 0 {
		return nil
	}
	out := make(ObjectMetadata, len(m.Common)+len(m.MetadataOnly))
	maps.Copy(out, m.Common)
	maps.Copy(out, m.MetadataOnly)

	return out
}

// PutOptions are applied per-write by storage backends.
type PutOptions struct {
	Metadata ObjectMetadata
}

type PutOption func(*PutOptions)

// WithMetadata attaches custom metadata to the upload. nil/empty is a no-op.
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

// Apply reduces a list of options into a single PutOptions value.
func Apply(opts []PutOption) PutOptions {
	var p PutOptions
	for _, opt := range opts {
		opt(&p)
	}

	return p
}
