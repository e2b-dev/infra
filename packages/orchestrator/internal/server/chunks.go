package server

import (
	"context"
	"errors"

	"go.opentelemetry.io/otel/attribute"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/template/peerprovider"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

var (
	peerNotAvailable = &orchestrator.PeerAvailability{NotAvailable: true}
	peerUseStorage   = &orchestrator.PeerAvailability{UseStorage: true}
)

// seekableStreamSender implements peerprovider.Sender over a gRPC server stream (for seekable files).
type seekableStreamSender struct {
	stream orchestrator.ChunkService_GetBuildSeekableServer
}

func (s *seekableStreamSender) Send(data []byte) error {
	return s.stream.Send(&orchestrator.GetBuildSeekableResponse{Data: data})
}

// blobStreamSender implements peerprovider.Sender over a gRPC server stream (for blob files).
type blobStreamSender struct {
	stream orchestrator.ChunkService_GetBuildBlobServer
}

func (s *blobStreamSender) Send(data []byte) error {
	return s.stream.Send(&orchestrator.GetBuildBlobResponse{Data: data})
}

// toGRPCError translates peerprovider errors to gRPC status codes.
func toGRPCError(err error) error {
	switch {
	case errors.Is(err, peerprovider.ErrUnknownFile),
		errors.Is(err, peerprovider.ErrNotSupported):
		return status.Errorf(codes.InvalidArgument, "%v", err)
	default:
		return status.Errorf(codes.Internal, "%v", err)
	}
}

func (s *Server) GetBuildFileSize(ctx context.Context, req *orchestrator.GetBuildFileSizeRequest) (*orchestrator.GetBuildFileSizeResponse, error) {
	telemetry.SetAttributes(ctx, telemetry.WithBuildID(req.GetBuildId()), attribute.String("file_name", req.GetFileName()))

	if _, uploaded := s.uploadedBuilds.Load(req.GetBuildId()); uploaded {
		telemetry.SetAttributes(ctx, attribute.Bool("uploaded", true))

		return &orchestrator.GetBuildFileSizeResponse{Availability: peerUseStorage}, nil
	}

	src, err := peerprovider.ResolveSeekable(s.templateCache, req.GetBuildId(), req.GetFileName())
	if err != nil {
		if errors.Is(err, peerprovider.ErrNotAvailable) {
			return &orchestrator.GetBuildFileSizeResponse{Availability: peerNotAvailable}, nil
		}

		return nil, toGRPCError(err)
	}

	telemetry.ReportEvent(ctx, "getting file size", telemetry.WithBuildID(req.GetBuildId()), attribute.String("file_name", req.GetFileName()))

	size, err := src.Size(ctx)
	if err != nil {
		return nil, toGRPCError(err)
	}

	return &orchestrator.GetBuildFileSizeResponse{TotalSize: size}, nil
}

func (s *Server) GetBuildFileExists(ctx context.Context, req *orchestrator.GetBuildFileExistsRequest) (*orchestrator.GetBuildFileExistsResponse, error) {
	telemetry.SetAttributes(ctx, telemetry.WithBuildID(req.GetBuildId()), attribute.String("file_name", req.GetFileName()))

	if _, uploaded := s.uploadedBuilds.Load(req.GetBuildId()); uploaded {
		telemetry.SetAttributes(ctx, attribute.Bool("uploaded", true))

		return &orchestrator.GetBuildFileExistsResponse{Availability: peerUseStorage}, nil
	}

	src, err := peerprovider.ResolveBlob(s.templateCache, req.GetBuildId(), req.GetFileName())
	if err != nil {
		if errors.Is(err, peerprovider.ErrNotAvailable) {
			return &orchestrator.GetBuildFileExistsResponse{Availability: peerNotAvailable}, nil
		}

		return nil, toGRPCError(err)
	}

	exists, err := src.Exists(ctx)
	if err != nil {
		return nil, toGRPCError(err)
	}

	if !exists {
		return &orchestrator.GetBuildFileExistsResponse{Availability: peerNotAvailable}, nil
	}

	return &orchestrator.GetBuildFileExistsResponse{}, nil
}

// GetBuildSeekable streams a range from a seekable diff file (memfile, rootfs.ext4).
func (s *Server) GetBuildSeekable(req *orchestrator.GetBuildSeekableRequest, stream orchestrator.ChunkService_GetBuildSeekableServer) error {
	ctx := stream.Context()

	telemetry.SetAttributes(ctx,
		telemetry.WithBuildID(req.GetBuildId()),
		attribute.String("file_name", req.GetFileName()),
		attribute.Int64("offset", req.GetOffset()),
		attribute.Int64("length", req.GetLength()),
	)

	if _, uploaded := s.uploadedBuilds.Load(req.GetBuildId()); uploaded {
		telemetry.SetAttributes(ctx, attribute.Bool("uploaded", true))

		return stream.Send(&orchestrator.GetBuildSeekableResponse{Availability: peerUseStorage})
	}

	src, err := peerprovider.ResolveSeekable(s.templateCache, req.GetBuildId(), req.GetFileName())
	if err != nil {
		if errors.Is(err, peerprovider.ErrNotAvailable) {
			return stream.Send(&orchestrator.GetBuildSeekableResponse{Availability: peerNotAvailable})
		}

		return toGRPCError(err)
	}

	if err := src.Stream(ctx, req.GetOffset(), req.GetLength(), &seekableStreamSender{stream}); err != nil {
		if errors.Is(err, peerprovider.ErrNotAvailable) {
			return stream.Send(&orchestrator.GetBuildSeekableResponse{Availability: peerNotAvailable})
		}

		return toGRPCError(err)
	}

	return nil
}

// GetBuildBlob streams an entire blob file (snapfile, metadata, headers).
func (s *Server) GetBuildBlob(req *orchestrator.GetBuildBlobRequest, stream orchestrator.ChunkService_GetBuildBlobServer) error {
	ctx := stream.Context()

	telemetry.SetAttributes(ctx,
		telemetry.WithBuildID(req.GetBuildId()),
		attribute.String("file_name", req.GetFileName()),
	)

	if _, uploaded := s.uploadedBuilds.Load(req.GetBuildId()); uploaded {
		telemetry.SetAttributes(ctx, attribute.Bool("uploaded", true))

		return stream.Send(&orchestrator.GetBuildBlobResponse{Availability: peerUseStorage})
	}

	src, err := peerprovider.ResolveBlob(s.templateCache, req.GetBuildId(), req.GetFileName())
	if err != nil {
		if errors.Is(err, peerprovider.ErrNotAvailable) {
			return stream.Send(&orchestrator.GetBuildBlobResponse{Availability: peerNotAvailable})
		}

		return toGRPCError(err)
	}

	if err := src.Stream(ctx, &blobStreamSender{stream}); err != nil {
		if errors.Is(err, peerprovider.ErrNotAvailable) {
			return stream.Send(&orchestrator.GetBuildBlobResponse{Availability: peerNotAvailable})
		}

		return toGRPCError(err)
	}

	return nil
}
