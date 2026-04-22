package header

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sort"
	"sync/atomic"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

const NormalizeFixVersion = 3

// Dependency is the per-build metadata stored in V4 headers: size, checksum,
// and optional FrameData for compressed reads.
type Dependency struct {
	Size      int64
	Checksum  [32]byte
	FrameData *storage.FrameTable
}

type Header struct {
	Metadata *Metadata
	Mapping  []BuildMap

	initialDependencies map[uuid.UUID]Dependency
	dependencies        *utils.SetOnce[map[uuid.UUID]Dependency]
	locallyAvailable    atomic.Bool
}

func (t *Header) MarkLocallyAvailable() {
	if t == nil {
		return
	}
	t.locallyAvailable.Store(true)
}

// SerializeForV4Upload serializes this header with Metadata.Version bumped
// to V4 for wire format, without mutating the live header. Callers use
// this after FinalizeDependencies.
func (t *Header) SerializeForV4Upload() ([]byte, error) {
	meta := *t.Metadata
	meta.Version = MetadataVersionV4

	return serializeV4(&meta, t)
}

// NewHeader creates a minimal/empty header: no Dependencies map, born final.
// Use for V3 (uncompressed) paths, skeleton devices, and tests where a
// header just needs a Metadata + Mapping.
func NewHeader(metadata *Metadata, mapping []BuildMap) (*Header, error) {
	if metadata.BlockSize == 0 {
		return nil, errors.New("block size cannot be zero")
	}

	if len(mapping) == 0 {
		mapping = []BuildMap{{
			Offset:             0,
			Length:             metadata.Size,
			BuildId:            metadata.BuildId,
			BuildStorageOffset: 0,
		}}
	}

	deps := utils.NewSetOnce[map[uuid.UUID]Dependency]()
	_ = deps.SetValue(nil)

	return &Header{Metadata: metadata, Mapping: mapping, dependencies: deps}, nil
}

// NewHeaderWithKnownDependencies creates a born-final header. Used by the
// V4 deserializer and anywhere the full map is available up front.
func NewHeaderWithKnownDependencies(metadata *Metadata, mapping []BuildMap, knownDependencies map[uuid.UUID]Dependency) (*Header, error) {
	h, err := NewHeader(metadata, mapping)
	if err != nil {
		return nil, err
	}

	h.dependencies = utils.NewSetOnce[map[uuid.UUID]Dependency]()
	_ = h.dependencies.SetValue(knownDependencies)

	return h, nil
}

// NewHeaderWithPendingDependencies creates a header seeded with
// initialDependencies (typically the parent-inherited subset) and completed
// later via FinalizeDependencies. Until then, WaitForDependencies blocks.
// The header retains a reference to initialDependencies; callers should
// treat it as read-only after this call.
func NewHeaderWithPendingDependencies(metadata *Metadata, mapping []BuildMap, initialDependencies map[uuid.UUID]Dependency) (*Header, error) {
	h, err := NewHeader(metadata, mapping)
	if err != nil {
		return nil, err
	}

	h.initialDependencies = initialDependencies
	h.dependencies = utils.NewSetOnce[map[uuid.UUID]Dependency]()

	return h, nil
}

// IsPending reports whether the header is awaiting FinalizeDependencies.
func (t *Header) IsPending() bool {
	select {
	case <-t.dependencies.Done:
		return false
	default:
		return true
	}
}

// Done returns a channel closed when the header is finalized.
func (t *Header) Done() <-chan struct{} {
	return t.dependencies.Done
}

// WaitForDependencies blocks until the header is finalized and returns the
// per-build metadata. The returned map is the header's own; do not mutate.
func (t *Header) WaitForDependencies(ctx context.Context) (map[uuid.UUID]Dependency, error) {
	return t.dependencies.WaitWithContext(ctx)
}

// CancelOnError finalizes the header with err if err != nil. Designed for
// `defer h.CancelOnError(e)` with a named error return.
func (t *Header) CancelOnError(err error) {
	if t == nil || err == nil {
		return
	}

	_ = t.dependencies.SetError(err)
}

// FinalizeDependencies completes a pending header: merges extra into the
// parent-inherited initialDependencies and sets the result, unblocking
// WaitForDependencies. First call wins. Nil-safe: V3 uploader calls this on
// potentially-nil *Header references (see build_upload_v3.go).
func (t *Header) FinalizeDependencies(extra map[uuid.UUID]Dependency, err error) {
	if t == nil {
		return
	}
	if err != nil {
		t.CancelOnError(err)

		return
	}

	merged := make(map[uuid.UUID]Dependency, len(t.initialDependencies)+len(extra))
	maps.Copy(merged, t.initialDependencies)
	maps.Copy(merged, extra)
	_ = t.dependencies.SetValue(merged)
}

// referencedSubset returns entries of source referenced by any mapping.
func referencedSubset(mapping []BuildMap, source map[uuid.UUID]Dependency) map[uuid.UUID]Dependency {
	out := make(map[uuid.UUID]Dependency, min(len(source), len(mapping)))
	for _, m := range mapping {
		if _, dup := out[m.BuildId]; dup {
			continue
		}
		if d, ok := source[m.BuildId]; ok {
			out[m.BuildId] = d
		}
	}

	return out
}

func (t *Header) String() string {
	if t == nil {
		return "[nil Header]"
	}

	return fmt.Sprintf("[Header: version=%d, size=%d, blockSize=%d, generation=%d, buildId=%s, mappings=%d]",
		t.Metadata.Version,
		t.Metadata.Size,
		t.Metadata.BlockSize,
		t.Metadata.Generation,
		t.Metadata.BuildId.String(),
		len(t.Mapping),
	)
}

// IsNormalizeFixApplied is a helper method to soft fail for older versions of the header where fix for normalization was not applied.
// This should be removed in the future.
func (t *Header) IsNormalizeFixApplied() bool {
	return t.Metadata.Version >= NormalizeFixVersion
}

// GetShiftedMapping resolves a virtual offset to a build-local range.
func (t *Header) GetShiftedMapping(ctx context.Context, offset int64) (BuildMap, error) {
	mapping, shift, err := t.getMapping(ctx, offset)
	if err != nil {
		return BuildMap{}, err
	}
	mappedLength := int64(mapping.Length) - shift

	b := BuildMap{
		Offset:  mapping.BuildStorageOffset + uint64(shift),
		Length:  uint64(mappedLength),
		BuildId: mapping.BuildId,
	}

	if mappedLength < 0 {
		if t.IsNormalizeFixApplied() {
			return BuildMap{}, fmt.Errorf("mapped length for offset %d is negative: %d", offset, mappedLength)
		}

		b.Length = 0
		logger.L().Warn(ctx, "mapped length is negative, but normalize fix is not applied",
			zap.Int64("offset", offset),
			zap.Int64("mappedLength", mappedLength),
			logger.WithBuildID(mapping.BuildId.String()),
		)
	}

	return b, nil
}

func (t *Header) LookupDependency(ctx context.Context, buildID uuid.UUID) (Dependency, error) {
	if t.locallyAvailable.Load() && buildID == t.Metadata.BuildId {
		return Dependency{}, nil
	}

	if t.IsPending() {
		if dep, ok := t.initialDependencies[buildID]; ok {
			return dep, nil
		}
	}

	deps, err := t.dependencies.WaitWithContext(ctx)
	if err != nil {
		return Dependency{}, err
	}

	return deps[buildID], nil
}

func (t *Header) getMapping(ctx context.Context, offset int64) (*BuildMap, int64, error) {
	if offset < 0 || offset >= int64(t.Metadata.Size) {
		if t.IsNormalizeFixApplied() {
			return nil, 0, fmt.Errorf("offset %d is out of bounds (size: %d)", offset, t.Metadata.Size)
		}

		logger.L().Warn(ctx, "offset is out of bounds, but normalize fix is not applied",
			zap.Int64("offset", offset),
			zap.Int64("size", int64(t.Metadata.Size)),
			logger.WithBuildID(t.Metadata.BuildId.String()),
		)
	}
	if offset%int64(t.Metadata.BlockSize) != 0 {
		if t.IsNormalizeFixApplied() {
			return nil, 0, fmt.Errorf("offset %d is not aligned to block size %d", offset, t.Metadata.BlockSize)
		}

		logger.L().Warn(ctx, "offset is not aligned to block size, but normalize fix is not applied",
			zap.Int64("offset", offset),
			zap.Int64("blockSize", int64(t.Metadata.BlockSize)),
			logger.WithBuildID(t.Metadata.BuildId.String()),
		)
	}

	i := sort.Search(len(t.Mapping), func(i int) bool {
		return int64(t.Mapping[i].Offset) > offset
	})

	if i == 0 {
		return nil, 0, fmt.Errorf("no source found for offset %d", offset)
	}

	mapping := &t.Mapping[i-1]
	shift := offset - int64(mapping.Offset)

	// Verify that the offset falls within this mapping's range
	if shift >= int64(mapping.Length) {
		if t.IsNormalizeFixApplied() {
			return nil, 0, fmt.Errorf("offset %d is beyond the end of mapping at offset %d (ends at %d)",
				offset, mapping.Offset, mapping.Offset+mapping.Length)
		}

		logger.L().Warn(ctx, "offset is beyond the end of mapping, but normalize fix is not applied",
			zap.Int64("offset", offset),
			zap.Uint64("mappingOffset", mapping.Offset),
			zap.Uint64("mappingEnd", mapping.Offset+mapping.Length),
			logger.WithBuildID(t.Metadata.BuildId.String()),
		)
	}

	return mapping, shift, nil
}

// ValidateHeader checks header integrity and returns an error if corruption is detected.
// This verifies:
// 1. Header and metadata are valid
// 2. Mappings cover the entire file [0, Size) with no gaps
// 3. Mappings don't extend beyond file size (with block alignment tolerance)
func ValidateHeader(h *Header) error {
	if h == nil {
		return errors.New("header is nil")
	}
	if h.Metadata == nil {
		return errors.New("header metadata is nil")
	}
	if h.Metadata.BlockSize == 0 {
		return errors.New("header has zero block size")
	}
	if h.Metadata.Size == 0 {
		return errors.New("header has zero size")
	}
	if len(h.Mapping) == 0 {
		return errors.New("header has no mappings")
	}

	// Sort mappings by offset to check for gaps/overlaps
	sortedMappings := slices.Clone(h.Mapping)
	slices.SortFunc(sortedMappings, func(a, b BuildMap) int {
		return cmp.Compare(a.Offset, b.Offset)
	})

	// Check that first mapping starts at 0
	if sortedMappings[0].Offset != 0 {
		return fmt.Errorf("mappings don't start at 0: first mapping starts at %d for buildId %s",
			sortedMappings[0].Offset, h.Metadata.BuildId.String())
	}

	// Check for gaps and overlaps between consecutive mappings
	for i := range len(sortedMappings) - 1 {
		currentEnd := sortedMappings[i].Offset + sortedMappings[i].Length
		nextStart := sortedMappings[i+1].Offset

		if currentEnd < nextStart {
			return fmt.Errorf("gap in mappings: mapping[%d] ends at %d but mapping[%d] starts at %d (gap=%d bytes) for buildId %s",
				i, currentEnd, i+1, nextStart, nextStart-currentEnd, h.Metadata.BuildId.String())
		}
		if currentEnd > nextStart {
			return fmt.Errorf("overlap in mappings: mapping[%d] ends at %d but mapping[%d] starts at %d (overlap=%d bytes) for buildId %s",
				i, currentEnd, i+1, nextStart, currentEnd-nextStart, h.Metadata.BuildId.String())
		}
	}

	// Check that last mapping covers up to (at least) Size
	lastMapping := sortedMappings[len(sortedMappings)-1]
	lastEnd := lastMapping.Offset + lastMapping.Length
	if lastEnd < h.Metadata.Size {
		return fmt.Errorf("mappings don't cover entire file: last mapping ends at %d but file size is %d (missing %d bytes) for buildId %s",
			lastEnd, h.Metadata.Size, h.Metadata.Size-lastEnd, h.Metadata.BuildId.String())
	}

	// Allow last mapping to extend up to one block past size (for alignment)
	if lastEnd > h.Metadata.Size+h.Metadata.BlockSize {
		return fmt.Errorf("last mapping extends too far: ends at %d but file size is %d (overhang=%d bytes, max allowed=%d) for buildId %s",
			lastEnd, h.Metadata.Size, lastEnd-h.Metadata.Size, h.Metadata.BlockSize, h.Metadata.BuildId.String())
	}

	// Validate individual mapping bounds
	for i, m := range h.Mapping {
		if m.Offset > h.Metadata.Size {
			return fmt.Errorf("mapping[%d] has Offset %d beyond header size %d for buildId %s",
				i, m.Offset, h.Metadata.Size, m.BuildId.String())
		}
		if m.Length == 0 {
			return fmt.Errorf("mapping[%d] has zero length at offset %d for buildId %s",
				i, m.Offset, m.BuildId.String())
		}
	}

	return nil
}
