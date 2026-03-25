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

var _ storage.FramedFile = (*peerFramedFile)(nil)

// peerFramedFile reads from the peer orchestrator first.
// During P2P, all reads use ft=nil (uncompressed) — the peer serves from
// its mmap cache which contains uncompressed data from the snapshot.
// After upload completes, reads fall through to the base GCS-backed FramedFile.
type peerFramedFile struct {
	peerHandle[storage.FramedFile]
}

func (f *peerFramedFile) Size(ctx context.Context) (int64, error) {
	return withPeerFallback(ctx, &f.peerHandle, "size peer-framedfile", attrOpSize,
		func(ctx context.Context) (peerAttempt[int64], error) {
			resp, err := f.client.GetBuildFileSize(ctx, &orchestrator.GetBuildFileSizeRequest{
				BuildId:  f.buildID,
				FileName: f.fileName,
			})
			if err == nil && checkPeerAvailability(resp.GetAvailability(), f.uploaded) {
				return peerAttempt[int64]{value: resp.GetTotalSize(), hit: true}, nil
			}

			if err != nil {
				logger.L().Warn(ctx, "failed to get build file size from peer", logger.WithBuildID(f.buildID), zap.Error(err))
			}

			return peerAttempt[int64]{}, nil
		},
		func(ctx context.Context, base storage.FramedFile) (int64, error) {
			return base.Size(ctx)
		},
	)
}

func (f *peerFramedFile) GetFrame(ctx context.Context, offsetU int64, frameTable *storage.FrameTable, decompress bool,
	buf []byte, readSize int64, onRead func(totalWritten int64),
) (storage.Range, error) {
	return withPeerFallback(ctx, &f.peerHandle, "get-frame peer-framedfile", attrOpGetFrame,
		func(ctx context.Context) (peerAttempt[storage.Range], error) {
			recv, err := openPeerFramedStream(ctx, f.client, &orchestrator.GetBuildFrameRequest{
				BuildId:  f.buildID,
				FileName: f.fileName,
				Offset:   offsetU,
				Length:   readSize,
			}, f.uploaded)
			if err != nil {
				logger.L().Warn(ctx, "failed to read build file from peer", logger.WithBuildID(f.buildID), zap.Int64("off", offsetU), zap.Int64("read_size", readSize), zap.Error(err))

				return peerAttempt[storage.Range]{}, nil
			}

			n := 0

			for n < int(readSize) && n < len(buf) {
				data, recvErr := recv()
				if errors.Is(recvErr, io.EOF) {
					break
				}

				if recvErr != nil {
					return peerAttempt[storage.Range]{
						value: storage.Range{Length: n},
						bytes: int64(n),
						hit:   true,
					}, fmt.Errorf("failed to receive chunk from peer: %w", recvErr)
				}

				copied := copy(buf[n:], data)
				n += copied
			}

			if onRead != nil {
				onRead(int64(n))
			}

			return peerAttempt[storage.Range]{
				value: storage.Range{Start: offsetU, Length: n},
				bytes: int64(n),
				hit:   true,
			}, nil
		},
		func(ctx context.Context, base storage.FramedFile) (storage.Range, error) {
			// If the upload completed and V4 headers are available, signal the
			// caller to swap its header and retry. When headers are empty
			// (uncompressed builds), fall through to base — no swap needed.
			if f.uploaded != nil {
				if hdrs := f.uploaded.Load(); hdrs != nil && (len(hdrs.MemfileHeader) > 0 || len(hdrs.RootfsHeader) > 0) {
					return storage.Range{}, &storage.PeerTransitionedError{
						MemfileHeader: hdrs.MemfileHeader,
						RootfsHeader:  hdrs.RootfsHeader,
					}
				}
			}

			return base.GetFrame(ctx, offsetU, frameTable, decompress, buf, readSize, onRead)
		},
	)
}

func (f *peerFramedFile) StoreFile(ctx context.Context, path string, cfg *storage.CompressConfig) (*storage.FrameTable, [32]byte, error) {
	// Writes always go to the base provider (GCS/S3); the peer is read-only.
	fallback, err := f.getOrOpenBase(ctx)
	if err != nil {
		return nil, [32]byte{}, err
	}

	return fallback.StoreFile(ctx, path, cfg)
}

// openPeerFramedStream opens a GetBuildFrame stream, checks peer availability,
// and returns a recv function that yields data chunks starting with the first message's data.
func openPeerFramedStream(
	ctx context.Context,
	client orchestrator.ChunkServiceClient,
	req *orchestrator.GetBuildFrameRequest,
	uploaded *atomic.Pointer[UploadedHeaders],
) (func() ([]byte, error), error) {
	stream, err := client.GetBuildFrame(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("open framed stream: %w", err)
	}

	msg, err := stream.Recv()
	if err != nil {
		return nil, fmt.Errorf("recv first framed message: %w", err)
	}

	if !checkPeerAvailability(msg.GetAvailability(), uploaded) {
		return nil, fmt.Errorf("peer not available for framed stream")
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
