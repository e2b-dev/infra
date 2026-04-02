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

func (s *peerSeekable) ReadAt(ctx context.Context, buf []byte, off int64) (int, error) {
	return withPeerFallback(ctx, &s.peerHandle, "read-at peer-seekable", attrOpReadAt,
		func(ctx context.Context) (peerAttempt[int], error) {
			recv, err := openPeerSeekableStream(ctx, s.client, &orchestrator.ReadAtBuildSeekableRequest{
				BuildId:  s.buildID,
				FileName: s.fileName,
				Offset:   off,
				Length:   int64(len(buf)),
			}, s.uploaded)
			if err != nil {
				logger.L().Warn(ctx, "failed to read build file from peer", logger.WithBuildID(s.buildID), zap.Int64("off", off), zap.Int("buf_len", len(buf)), zap.Error(err))

				return peerAttempt[int]{}, nil
			}

			n := 0

			for n < len(buf) {
				data, recvErr := recv()
				if errors.Is(recvErr, io.EOF) {
					break
				}

				if recvErr != nil {
					return peerAttempt[int]{value: n, bytes: int64(n), hit: true},
						fmt.Errorf("failed to receive chunk from peer: %w", recvErr)
				}

				n += copy(buf[n:], data)
			}

			if n < len(buf) {
				return peerAttempt[int]{value: n, bytes: int64(n), hit: true}, io.ErrUnexpectedEOF
			}

			return peerAttempt[int]{value: n, bytes: int64(n), hit: true}, nil
		},
		func(ctx context.Context, base storage.Seekable) (int, error) {
			rc, err := base.OpenRangeReader(ctx, off, int64(len(buf)), nil)
			if err != nil {
				return 0, err
			}
			defer rc.Close()

			return io.ReadFull(rc, buf)
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
			// Signal the caller to swap to V4 headers if compressed headers are available.
			if s.uploaded != nil {
				if hdrs := s.uploaded.Load(); hdrs != nil && (len(hdrs.MemfileHeader) > 0 || len(hdrs.RootfsHeader) > 0) {
					return nil, &storage.PeerTransitionedError{
						MemfileHeader: hdrs.MemfileHeader,
						RootfsHeader:  hdrs.RootfsHeader,
					}
				}
			}

			return base.OpenRangeReader(ctx, off, length, frameTable)
		},
	)
}

func (s *peerSeekable) StoreFile(ctx context.Context, path string, cfg *storage.CompressConfig) (*storage.FrameTable, [32]byte, error) {
	// Writes always go to the base provider (GCS/S3); the peer is read-only.
	fallback, err := s.getOrOpenBase(ctx)
	if err != nil {
		return nil, [32]byte{}, err
	}

	return fallback.StoreFile(ctx, path, cfg)
}

// openPeerSeekableStream opens a ReadAtBuildSeekable stream, checks peer availability,
// and returns a recv function that yields data chunks starting with the first message's data.
func openPeerSeekableStream(
	ctx context.Context,
	client orchestrator.ChunkServiceClient,
	req *orchestrator.ReadAtBuildSeekableRequest,
	uploaded *atomic.Pointer[UploadedHeaders],
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
		return nil, fmt.Errorf("peer not available for seekable stream")
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
