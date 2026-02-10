package header

import (
	"context"
	"fmt"
	"io"

	"github.com/bits-and-blooms/bitset"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

var ignoreBuildID = uuid.Nil

type DiffMetadata struct {
	Dirty *bitset.BitSet
	Empty *bitset.BitSet

	BlockSize int64
}

func (d *DiffMetadata) toDiffMapping(
	ctx context.Context,
	buildID uuid.UUID,
) (mapping []*BuildMap) {
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
		attribute.Int64("snapshot.diff.size", int64(d.Dirty.Count()*uint(originalHeader.Metadata.BlockSize))),
		attribute.Int64("snapshot.mapped_size", int64(metadata.Size)),
		attribute.Int64("snapshot.block_size", int64(metadata.BlockSize)),
		attribute.Int64("snapshot.metadata.version", int64(metadata.Version)),
		attribute.Int64("snapshot.metadata.generation", int64(metadata.Generation)),
		attribute.String("snapshot.metadata.build_id", metadata.BuildId.String()),
		attribute.String("snapshot.metadata.base_build_id", metadata.BaseBuildId.String()),
	)

	header, err := NewHeader(metadata, m)
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
	dirty *bitset.BitSet
	empty *bitset.BitSet

	blockSize int64
}

func NewDiffMetadataBuilder(size, blockSize int64) *DiffMetadataBuilder {
	return &DiffMetadataBuilder{
		dirty: bitset.New(uint(TotalBlocks(size, blockSize))),
		empty: bitset.New(0),

		blockSize: blockSize,
	}
}

func (b *DiffMetadataBuilder) Process(ctx context.Context, block []byte, out io.Writer, offset int64) error {
	blockIdx := BlockIdx(offset, b.blockSize)

	isEmpty, err := IsEmptyBlock(block, b.blockSize)
	if err != nil {
		return fmt.Errorf("error checking empty block: %w", err)
	}
	if isEmpty {
		b.empty.Set(uint(blockIdx))

		return nil
	}

	b.dirty.Set(uint(blockIdx))
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
