package header

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"

	"github.com/RoaringBitmap/roaring/v2"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const (
	// metadataVersion is used by template-manager for uncompressed builds (V3 headers).
	metadataVersion = 3
	// MetadataVersionV4 is used for compressed builds (V4 headers with FrameTables).
	MetadataVersionV4 = 4
)

type Metadata struct {
	Version    uint64
	BlockSize  uint64
	Size       uint64
	Generation uint64
	BuildId    uuid.UUID
	// TODO: Use the base build id when setting up the snapshot rootfs
	BaseBuildId uuid.UUID
}

func NewTemplateMetadata(buildId uuid.UUID, blockSize, size uint64) *Metadata {
	return &Metadata{
		Version:     metadataVersion,
		Generation:  0,
		BlockSize:   blockSize,
		Size:        size,
		BuildId:     buildId,
		BaseBuildId: buildId,
	}
}

func (m *Metadata) NextGeneration(buildID uuid.UUID) *Metadata {
	return &Metadata{
		Version:     m.Version,
		Generation:  m.Generation + 1,
		BlockSize:   m.BlockSize,
		Size:        m.Size,
		BuildId:     buildID,
		BaseBuildId: m.BaseBuildId,
	}
}

// metadataSize is the binary size of the Metadata struct, computed from the struct layout.
var metadataSize = binary.Size(Metadata{})

func deserializeMetadata(data []byte) (*Metadata, error) {
	var metadata Metadata

	err := binary.Read(bytes.NewReader(data), binary.LittleEndian, &metadata)
	if err != nil {
		return nil, fmt.Errorf("failed to read metadata: %w", err)
	}

	return &metadata, nil
}

var ignoreBuildID = uuid.Nil

type DiffMetadata struct {
	Dirty *roaring.Bitmap
	Empty *roaring.Bitmap

	BlockSize int64
}

func NewDiffMetadata(blockSize int64, dirty *roaring.Bitmap) *DiffMetadata {
	return &DiffMetadata{
		Dirty:     dirty,
		Empty:     roaring.New(),
		BlockSize: blockSize,
	}
}

func (d *DiffMetadata) toDiffMapping(
	ctx context.Context,
	buildID uuid.UUID,
) (mapping []BuildMap) {
	dirtyMappings := CreateMapping(
		&buildID,
		d.Dirty,
		d.BlockSize,
	)
	telemetry.ReportEvent(ctx, "created dirty mapping")

	emptyMappings := CreateMapping(
		// This buildID is intentionally ignored for nil blocks
		&ignoreBuildID,
		d.Empty,
		d.BlockSize,
	)
	telemetry.ReportEvent(ctx, "created empty mapping")

	mappings := MergeMappings(dirtyMappings, emptyMappings)
	telemetry.ReportEvent(ctx, "merge mappings")

	return mappings
}

func (d *DiffMetadata) ToDiffHeader(
	ctx context.Context,
	originalHeader *Header,
	buildID uuid.UUID,
) (h *Header, e error) {
	ctx, span := tracer.Start(ctx, "to diff-header")
	defer span.End()
	defer func() {
		if e != nil {
			span.RecordError(e)
			span.SetStatus(codes.Error, e.Error())
		}
	}()

	diffMapping := d.toDiffMapping(ctx, buildID)

	m := MergeMappings(
		originalHeader.Mapping,
		diffMapping,
	)
	telemetry.ReportEvent(ctx, "merged mappings")

	// TODO: We can run normalization only when empty mappings are not empty for this snapshot
	m = NormalizeMappings(m)
	telemetry.ReportEvent(ctx, "normalized mappings")

	metadata := originalHeader.Metadata.NextGeneration(buildID)

	telemetry.SetAttributes(ctx,
		attribute.Int64("snapshot.header.mappings.length", int64(len(m))),
		attribute.Int64("snapshot.diff.size", int64(d.Dirty.GetCardinality())*int64(originalHeader.Metadata.BlockSize)),
		attribute.Int64("snapshot.mapped_size", int64(metadata.Size)),
		attribute.Int64("snapshot.block_size", int64(metadata.BlockSize)),
		attribute.Int64("snapshot.metadata.version", int64(metadata.Version)),
		attribute.Int64("snapshot.metadata.generation", int64(metadata.Generation)),
		attribute.String("snapshot.metadata.build_id", metadata.BuildId.String()),
		attribute.String("snapshot.metadata.base_build_id", metadata.BaseBuildId.String()),
	)

	header, err := newDiffHeader(metadata, m, originalHeader.Builds)
	if err != nil {
		return nil, fmt.Errorf("failed to create header: %w", err)
	}

	err = ValidateMappings(header.Mapping, header.Metadata.Size, header.Metadata.BlockSize)
	if err != nil {
		if header.IsNormalizeFixApplied() {
			return nil, fmt.Errorf("invalid header mappings: %w", err)
		}

		logger.L().Warn(ctx, "header mappings are invalid, but normalize fix is not applied", zap.Error(err), logger.WithBuildID(header.Metadata.BuildId.String()))
	}

	return header, nil
}

type DiffMetadataBuilder struct {
	dirty *roaring.Bitmap
	empty *roaring.Bitmap

	blockSize int64
}

func NewDiffMetadataBuilder(blockSize int64) *DiffMetadataBuilder {
	return &DiffMetadataBuilder{
		dirty: roaring.New(),
		empty: roaring.New(),

		blockSize: blockSize,
	}
}

func (b *DiffMetadataBuilder) Process(ctx context.Context, block []byte, out io.Writer, offset int64) error {
	blockIdx := BlockIdx(offset, b.blockSize)

	if IsZero(block) {
		b.empty.Add(uint32(blockIdx))

		return nil
	}

	b.dirty.Add(uint32(blockIdx))
	n, err := out.Write(block)
	if err != nil {
		logger.L().Error(ctx, "error writing to out", zap.Error(err))

		return err
	}

	if int64(n) != b.blockSize {
		return fmt.Errorf("short write: %d != %d", int64(n), b.blockSize)
	}

	return nil
}

func (b *DiffMetadataBuilder) Build() *DiffMetadata {
	return &DiffMetadata{
		Dirty:     b.dirty,
		Empty:     b.empty,
		BlockSize: b.blockSize,
	}
}
