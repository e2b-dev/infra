package peerclient

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync/atomic"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

var _ storage.Seekable = (*peerSeekable)(nil)

// peerSeekable reads from the peer orchestrator first.
// calls (e.g. ReadAt then OpenRangeReader) do not re-open the underlying GCS object.
type peerSeekable struct {
	peerHandle[storage.Seekable]

	// transitionEmitted ensures we signal PeerTransitionedError at most once
	// after the peer flips uploaded=true. The caller (build.File) reacts by
	// loading the post-upload header from storage; whether that ends up V4
	// (compressed) or V3 (no upgrade) determines how subsequent reads route.
	// Either way, after the first emission we fall through to base so V3
	// builds don't loop forever against PeerTransitionedError.
	transitionEmitted atomic.Bool
}

func (s *peerSeekable) Size(ctx context.Context) (int64, error) {
	return withPeerFallback(ctx, &s.peerHandle, "size peer-seekable", attrOpSize,
		func(ctx context.Context) (peerAttempt[int64], error) {
			resp, err := s.client.GetBuildFileSize(ctx, &orchestrator.GetBuildFileSizeRequest{
				BuildId:  s.buildID,
				FileName: s.fileName,
			})
			if err == nil && checkPeerAvailability(resp.GetAvailability(), s.uploaded) {
				return peerAttempt[int64]{value: resp.GetTotalSize(), hit: true}, nil
			}

			if err != nil {
				logger.L().Warn(ctx, "failed to get build file size from peer", logger.WithBuildID(s.buildID), zap.Error(err))
			}

			return peerAttempt[int64]{}, nil
		},
		func(ctx context.Context, base storage.Seekable) (int64, error) {
			return base.Size(ctx)
		},
	)
}

func (s *peerSeekable) OpenRangeReader(ctx context.Context, off int64, length int64, frameTable *storage.FrameTable) (io.ReadCloser, error) {
	return withPeerFallback(ctx, &s.peerHandle, "peer-seekable-open-range-reader", attrOpRangeReader,
		func(ctx context.Context) (peerAttempt[io.ReadCloser], error) {
			streamCtx, cancel := context.WithCancel(ctx)

			recv, err := openPeerSeekableStream(streamCtx, s.client, &orchestrator.ReadAtBuildSeekableRequest{
				BuildId:  s.buildID,
				FileName: s.fileName,
				Offset:   off,
				Length:   length,
			}, s.uploaded)
			if err != nil {
				logger.L().Warn(ctx, "failed to open range reader from peer", logger.WithBuildID(s.buildID), zap.Int64("off", off), zap.Int64("length", length), zap.Error(err))
				cancel()

				return peerAttempt[io.ReadCloser]{}, nil
			}

			return peerAttempt[io.ReadCloser]{
				value: newPeerStreamReader(recv, cancel),
				hit:   true,
			}, nil
		},
		func(ctx context.Context, base storage.Seekable) (io.ReadCloser, error) {
			// Signal the caller once to fetch the post-upload header from storage;
			// thereafter fall through so V3 builds (no V4 to upgrade to) don't
			// loop against PeerTransitionedError.
			if s.uploaded != nil && s.uploaded.Load() && s.transitionEmitted.CompareAndSwap(false, true) {
				return nil, &storage.PeerTransitionedError{}
			}

			return base.OpenRangeReader(ctx, off, length, frameTable)
		},
	)
}

func (s *peerSeekable) StoreFile(ctx context.Context, path string, opts ...storage.PutOption) (*storage.FrameTable, [32]byte, error) {
	// Writes always go to the base provider (GCS/S3); the peer is read-only.
	fallback, err := s.getOrOpenBase(ctx)
	if err != nil {
		return nil, [32]byte{}, err
	}

	return fallback.StoreFile(ctx, path, opts...)
}

// openPeerSeekableStream opens a ReadAtBuildSeekable stream, checks peer availability,
// and returns a recv function that yields data chunks starting with the first message's data.
// The passed context HAS to be canceled by the caller when done with the stream to avoid leaks.
func openPeerSeekableStream(
	ctx context.Context,
	client orchestrator.ChunkServiceClient,
	req *orchestrator.ReadAtBuildSeekableRequest,
	uploaded *atomic.Bool,
) (func() ([]byte, error), error) {
	stream, err := client.ReadAtBuildSeekable(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("open seekable stream: %w", err)
	}

	msg, err := stream.Recv()
	if err != nil {
		return nil, fmt.Errorf("recv first seekable message: %w", err)
	}

	if !checkPeerAvailability(msg.GetAvailability(), uploaded) {
		return nil, errors.New("peer not available for seekable stream")
	}

	first := msg.GetData()

	return func() ([]byte, error) {
		if first != nil {
			data := first
			first = nil

			return data, nil
		}

		m, err := stream.Recv()
		if err != nil {
			return nil, err
		}

		// Flip the uploaded flag if the peer signals use_storage; the current
		// stream keeps reading from the peer, but subsequent operations will
		// go directly to GCS.
		checkPeerAvailability(m.GetAvailability(), uploaded)

		return m.GetData(), nil
	}, nil
}
