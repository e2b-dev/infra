package peerclient

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

var _ storage.Seekable = (*peerSeekable)(nil)

// peerSeekable reads from the peer orchestrator first.
// Peer fetches always use the basic (uncompressed) name. Only the base
// (GCS/S3) fallthrough path needs to know the current compression type —
// it's resolved per call from the live FrameTable, so a header swap from
// V3 to V4 (or vice versa) is reflected on the next read.
type peerSeekable struct {
	peerHandle

	basePersistence storage.StorageProvider
	objType         storage.SeekableObjectType

	mu     sync.Mutex
	base   storage.Seekable
	baseCT storage.CompressionType
	loaded bool

	// transitionEmitted ensures we signal PeerTransitionedError at most once
	// after the peer flips uploaded=true. The caller (build.File) reacts by
	// loading the post-upload header from storage; whether that ends up V4
	// (compressed) or V3 (no upgrade) determines how subsequent reads route.
	// Either way, after the first emission we fall through to base so V3
	// builds don't loop forever against PeerTransitionedError.
	transitionEmitted atomic.Bool
}

// getBase returns a base Seekable opened against the storage path composed
// from (buildID, basic name, ct). Reopens if ct differs from the cached
// entry — a no-op for V3 (always None) but essential after a V3→V4 swap.
func (s *peerSeekable) getBase(ctx context.Context, ct storage.CompressionType) (storage.Seekable, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.loaded && s.baseCT == ct {
		return s.base, nil
	}

	path := storage.Paths{BuildID: s.buildID}.DataFile(s.fileName, ct)

	base, err := s.basePersistence.OpenSeekable(ctx, path, s.objType)
	if err != nil {
		return nil, err
	}

	s.base = base
	s.baseCT = ct
	s.loaded = true

	return base, nil
}

func (s *peerSeekable) Size(ctx context.Context) (int64, error) {
	res, err := tryPeer(ctx, &s.peerHandle, "size peer-seekable", attrOpSize,
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
		})
	if res.hit {
		return res.value, err
	}

	// Size only reaches base for V3 builds (uncompressedSize unknown);
	// V4 builds carry the size in the header so the chunker never calls Size.
	// V3 implies CompressionNone, matching reality.
	base, err := s.getBase(ctx, storage.CompressionNone)
	if err != nil {
		return 0, err
	}

	return base.Size(ctx)
}

func (s *peerSeekable) OpenRangeReader(ctx context.Context, off int64, length int64, frameTable *storage.FrameTable) (io.ReadCloser, error) {
	// Once the peer flips uploaded=true, ask the caller to refresh its header
	// from storage (the post-upload state may be V4/compressed) and retry.
	// Emit at most once per peerSeekable so V3 builds (no V4 to upgrade to)
	// don't loop against this error. No peer call, no base open — caller
	// retries with the live compression type already in the new FrameTable.
	if s.uploaded.Load() && s.transitionEmitted.CompareAndSwap(false, true) {
		return nil, &storage.PeerTransitionedError{}
	}

	res, err := tryPeer(ctx, &s.peerHandle, "peer-seekable-open-range-reader", attrOpRangeReader,
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
		})
	if res.hit {
		return res.value, err
	}

	ct := storage.CompressionNone
	if frameTable != nil {
		ct = frameTable.CompressionType()
	}

	base, err := s.getBase(ctx, ct)
	if err != nil {
		return nil, err
	}

	return base.OpenRangeReader(ctx, off, length, frameTable)
}

func (s *peerSeekable) StoreFile(ctx context.Context, path string, opts ...storage.PutOption) (*storage.FrameTable, [32]byte, error) {
	// Writes always go to the base provider (GCS/S3); the peer is read-only.
	// StoreFile composes its own paths via FrameTable; ct here is irrelevant
	// to the actual write target.
	fallback, err := s.getBase(ctx, storage.CompressionNone)
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
