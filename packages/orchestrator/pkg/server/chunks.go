package server

import (
	"context"
	"errors"

	"go.opentelemetry.io/otel/attribute"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/template/peerserver"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

var peerNotAvailable = &orchestrator.PeerAvailability{NotAvailable: true}

// framedStreamSender implements peerserver.Sender over a gRPC server stream (for framed files).
type framedStreamSender struct {
	stream orchestrator.ChunkService_GetBuildFrameServer
}

func (s *framedStreamSender) Send(data []byte) error {
	return s.stream.Send(&orchestrator.GetBuildFrameResponse{Data: data})
}

// blobStreamSender implements peerserver.Sender over a gRPC server stream (for blob files).
type blobStreamSender struct {
	stream orchestrator.ChunkService_GetBuildBlobServer
}

func (s *blobStreamSender) Send(data []byte) error {
	return s.stream.Send(&orchestrator.GetBuildBlobResponse{Data: data})
}

// toGRPCError translates peerserver errors to gRPC status codes.
func toGRPCError(err error) error {
	switch {
	case errors.Is(err, peerserver.ErrUnknownFile),
		errors.Is(err, peerserver.ErrNotSupported):
		return status.Errorf(codes.InvalidArgument, "%v", err)
	default:
		return status.Errorf(codes.Internal, "%v", err)
	}
}

func (s *Server) buildUploadedResponse(buildID string) *orchestrator.PeerAvailability {
	item := s.uploadedBuilds.Get(buildID)
	if item == nil {
		return nil
	}

	hdrs := item.Value()

	return &orchestrator.PeerAvailability{
		UseStorage:    true,
		MemfileHeader: hdrs.memfileHeader,
		RootfsHeader:  hdrs.rootfsHeader,
	}
}

func (s *Server) GetBuildFileSize(ctx context.Context, req *orchestrator.GetBuildFileSizeRequest) (*orchestrator.GetBuildFileSizeResponse, error) {
	telemetry.SetAttributes(ctx, telemetry.WithBuildID(req.GetBuildId()), attribute.String("file_name", req.GetFileName()))

	if avail := s.buildUploadedResponse(req.GetBuildId()); avail != nil {
		telemetry.SetAttributes(ctx, attribute.Bool("uploaded", true))

		return &orchestrator.GetBuildFileSizeResponse{Availability: avail}, nil
	}

	src, err := peerserver.ResolveFramed(s.templateCache, req.GetBuildId(), req.GetFileName())
	if err != nil {
		if errors.Is(err, peerserver.ErrNotAvailable) {
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

	if avail := s.buildUploadedResponse(req.GetBuildId()); avail != nil {
		telemetry.SetAttributes(ctx, attribute.Bool("uploaded", true))

		return &orchestrator.GetBuildFileExistsResponse{Availability: avail}, nil
	}

	src, err := peerserver.ResolveBlob(s.templateCache, req.GetBuildId(), req.GetFileName())
	if err != nil {
		if errors.Is(err, peerserver.ErrNotAvailable) {
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

// GetBuildFrame streams a range from a framed diff file (memfile, rootfs.ext4).
func (s *Server) GetBuildFrame(req *orchestrator.GetBuildFrameRequest, stream orchestrator.ChunkService_GetBuildFrameServer) error {
	ctx := stream.Context()
	offset := req.GetOffset()
	length := req.GetLength()

	if offset < 0 || length < 0 {
		return status.Error(codes.InvalidArgument, "offset and length must be non-negative")
	}

	telemetry.SetAttributes(ctx,
		telemetry.WithBuildID(req.GetBuildId()),
		attribute.String("file_name", req.GetFileName()),
		attribute.Int64("offset", offset),
		attribute.Int64("length", length),
	)

	if avail := s.buildUploadedResponse(req.GetBuildId()); avail != nil {
		telemetry.SetAttributes(ctx, attribute.Bool("uploaded", true))

		return stream.Send(&orchestrator.GetBuildFrameResponse{Availability: avail})
	}

	src, err := peerserver.ResolveFramed(s.templateCache, req.GetBuildId(), req.GetFileName())
	if err != nil {
		if errors.Is(err, peerserver.ErrNotAvailable) {
			return stream.Send(&orchestrator.GetBuildFrameResponse{Availability: peerNotAvailable})
		}

		return toGRPCError(err)
	}

	if err := src.Stream(ctx, offset, length, &framedStreamSender{stream}); err != nil {
		if errors.Is(err, peerserver.ErrNotAvailable) {
			return stream.Send(&orchestrator.GetBuildFrameResponse{Availability: peerNotAvailable})
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

	if avail := s.buildUploadedResponse(req.GetBuildId()); avail != nil {
		telemetry.SetAttributes(ctx, attribute.Bool("uploaded", true))

		return stream.Send(&orchestrator.GetBuildBlobResponse{Availability: avail})
	}

	src, err := peerserver.ResolveBlob(s.templateCache, req.GetBuildId(), req.GetFileName())
	if err != nil {
		if errors.Is(err, peerserver.ErrNotAvailable) {
			return stream.Send(&orchestrator.GetBuildBlobResponse{Availability: peerNotAvailable})
		}

		return toGRPCError(err)
	}

	if err := src.Stream(ctx, &blobStreamSender{stream}); err != nil {
		if errors.Is(err, peerserver.ErrNotAvailable) {
			return stream.Send(&orchestrator.GetBuildBlobResponse{Availability: peerNotAvailable})
		}

		return toGRPCError(err)
	}

	return nil
}
