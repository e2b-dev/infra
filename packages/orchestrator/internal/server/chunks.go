package server

import (
	"context"
	"os"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/build"
	tmpl "github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/template"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

// chunkCache is the subset of template.Cache used by the ChunkService handlers.
type chunkCache interface {
	LookupDiff(buildID string, diffType build.DiffType) (tmpl.DiffSource, bool)
	GetCachedTemplate(buildID string) (tmpl.Template, bool)
}

// diffTypeFromFileName parses a file name into a build.DiffType.
// Returns the DiffType and true for memfile/rootfs seekable files, otherwise ("", false).
func diffTypeFromFileName(fileName string) (build.DiffType, bool) {
	dt := build.DiffType(fileName)
	switch dt {
	case build.Memfile, build.Rootfs:
		return dt, true
	default:
		return "", false
	}
}

func (s *Server) GetBuildFileInfo(ctx context.Context, req *orchestrator.GetBuildFileInfoRequest) (*orchestrator.GetBuildFileInfoResponse, error) {
	telemetry.SetAttributes(ctx, telemetry.WithBuildID(req.GetBuildId()), attribute.String("file_name", req.GetFileName()))

	if _, uploaded := s.uploadedBuilds.Load(req.GetBuildId()); uploaded {
		telemetry.SetAttributes(ctx, attribute.Bool("uploaded", true))

		return &orchestrator.GetBuildFileInfoResponse{UseStorage: true}, nil
	}

	return getBuildFileInfo(ctx, s.templateCache, req)
}

func getBuildFileInfo(ctx context.Context, cache chunkCache, req *orchestrator.GetBuildFileInfoRequest) (*orchestrator.GetBuildFileInfoResponse, error) {
	diffType, ok := diffTypeFromFileName(req.GetFileName())
	if !ok {
		return nil, status.Errorf(codes.InvalidArgument, "GetBuildFileInfo only supports seekable diff files, got %q", req.GetFileName())
	}

	diff, ok := cache.LookupDiff(req.GetBuildId(), diffType)
	if !ok {
		return &orchestrator.GetBuildFileInfoResponse{NotAvailable: true}, nil
	}

	telemetry.ReportEvent(ctx, "getting diff size", telemetry.WithBuildID(req.GetBuildId()), attribute.String("file_name", req.GetFileName()))
	size, err := diff.Size(ctx)
	if err != nil {
		telemetry.ReportError(ctx, "failed to get diff size", err)

		return nil, status.Errorf(codes.Internal, "failed to get diff size: %v", err)
	}

	return &orchestrator.GetBuildFileInfoResponse{TotalSize: size}, nil
}

func (s *Server) GetBuildFile(req *orchestrator.GetBuildFileRequest, stream orchestrator.ChunkService_GetBuildFileServer) error {
	ctx := stream.Context()

	telemetry.SetAttributes(ctx,
		telemetry.WithBuildID(req.GetBuildId()),
		attribute.String("file_name", req.GetFileName()),
		attribute.Int64("offset", req.GetOffset()),
		attribute.Int64("length", req.GetLength()),
	)

	if _, uploaded := s.uploadedBuilds.Load(req.GetBuildId()); uploaded {
		telemetry.SetAttributes(ctx, attribute.Bool("uploaded", true))

		return stream.Send(&orchestrator.GetBuildFileResponse{UseStorage: true})
	}

	return getBuildFile(ctx, s.templateCache, req, stream)
}

func getBuildFile(ctx context.Context, cache chunkCache, req *orchestrator.GetBuildFileRequest, stream orchestrator.ChunkService_GetBuildFileServer) error {
	switch fileName := req.GetFileName(); fileName {
	case storage.MemfileName, storage.RootfsName:
		diff, found := cache.LookupDiff(req.GetBuildId(), build.DiffType(fileName))
		if !found {
			return stream.Send(&orchestrator.GetBuildFileResponse{NotAvailable: true})
		}

		return streamDiff(ctx, diff, req.GetOffset(), req.GetLength(), stream)

	case storage.SnapfileName:
		t, ok := cache.GetCachedTemplate(req.GetBuildId())
		if !ok {
			return stream.Send(&orchestrator.GetBuildFileResponse{NotAvailable: true})
		}

		f, err := t.Snapfile()
		if err != nil {
			return status.Errorf(codes.Internal, "failed to get snapfile: %v", err)
		}

		return streamLocalFile(ctx, f.Path(), stream)

	case storage.MetadataName:
		t, ok := cache.GetCachedTemplate(req.GetBuildId())
		if !ok {
			return stream.Send(&orchestrator.GetBuildFileResponse{NotAvailable: true})
		}

		f, err := t.MetadataFile()
		if err != nil {
			return status.Errorf(codes.Internal, "failed to get metafile: %v", err)
		}

		return streamLocalFile(ctx, f.Path(), stream)

	case storage.MemfileName + storage.HeaderSuffix:
		t, ok := cache.GetCachedTemplate(req.GetBuildId())
		if !ok {
			return stream.Send(&orchestrator.GetBuildFileResponse{NotAvailable: true})
		}

		return streamHeader(ctx, func() (block.ReadonlyDevice, error) { return t.Memfile(ctx) }, stream)

	case storage.RootfsName + storage.HeaderSuffix:
		t, ok := cache.GetCachedTemplate(req.GetBuildId())
		if !ok {
			return stream.Send(&orchestrator.GetBuildFileResponse{NotAvailable: true})
		}

		return streamHeader(ctx, func() (block.ReadonlyDevice, error) { return t.Rootfs() }, stream)

	default:
		return status.Errorf(codes.InvalidArgument, "unknown file %q", fileName)
	}
}

func streamLocalFile(ctx context.Context, path string, stream orchestrator.ChunkService_GetBuildFileServer) error {
	_, span := tracer.Start(ctx, "stream-local-file", trace.WithAttributes(
		attribute.String("path", path),
	))
	defer span.End()

	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return stream.Send(&orchestrator.GetBuildFileResponse{NotAvailable: true})
		}

		span.RecordError(err)
		return status.Errorf(codes.Internal, "failed to open file %q: %v", path, err)
	}
	defer f.Close()

	return copyToStream(span, f, stream)
}

func streamHeader(ctx context.Context, getDevice func() (block.ReadonlyDevice, error), stream orchestrator.ChunkService_GetBuildFileServer) error {
	_, span := tracer.Start(ctx, "serialize-and-send-header")
	defer span.End()

	device, err := getDevice()
	if err != nil {
		span.RecordError(err)
		return status.Errorf(codes.Internal, "failed to get device: %v", err)
	}

	h := device.Header()
	if h == nil {
		return stream.Send(&orchestrator.GetBuildFileResponse{NotAvailable: true})
	}

	data, err := header.Serialize(h.Metadata, h.Mapping)
	if err != nil {
		span.RecordError(err)
		return status.Errorf(codes.Internal, "failed to serialize header: %v", err)
	}

	return stream.Send(&orchestrator.GetBuildFileResponse{Data: data})
}

func streamDiff(ctx context.Context, diff tmpl.DiffSource, off, length int64, stream orchestrator.ChunkService_GetBuildFileServer) error {
	_, span := tracer.Start(ctx, "stream-diff", trace.WithAttributes(
		attribute.Int64("offset", off),
		attribute.Int64("length", length),
	))
	defer span.End()

	data, err := diff.Slice(ctx, off, length)
	if err != nil {
		span.RecordError(err)
		return status.Errorf(codes.Internal, "failed to slice diff at offset %d: %v", off, err)
	}

	blockSize := int(diff.BlockSize())

	for len(data) > 0 {
		take := min(len(data), blockSize)
		if err := stream.Send(&orchestrator.GetBuildFileResponse{Data: data[:take]}); err != nil {
			span.RecordError(err)
			return status.Errorf(codes.Internal, "failed to send diff chunk: %v", err)
		}

		data = data[take:]
	}

	return nil
}
