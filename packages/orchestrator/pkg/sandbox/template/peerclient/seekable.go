package peerclient

import (
	"context"
	"fmt"
	"io"
	"sync"

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

	path := storage.Paths{BuildID: s.buildID}.DataFile(s.name, ct)

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
	size, hit, err := tryPeer(ctx, &s.peerHandle, "size peer-seekable", attrOpSize,
		func(ctx context.Context) (peerAttempt[int64], error) {
			resp, err := s.client.GetBuildFileSize(ctx, &orchestrator.GetBuildFileSizeRequest{
				BuildId: s.buildID,
				Name:    s.name,
			})
			if err != nil {
				logger.L().Warn(ctx, "failed to get build file size from peer", logger.WithBuildID(s.buildID), zap.Error(err))

				return peerAttempt[int64]{}, err
			}
			outcome := checkPeerAvailability(ctx, resp.GetAvailability(), s.state, s.name)
			if outcome != served {
				return peerAttempt[int64]{result: outcome}, nil
			}

			return peerAttempt[int64]{value: resp.GetTotalSize(), result: served}, nil
		})
	if hit {
		return size, err
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
	rc, hit, err := tryPeer(ctx, &s.peerHandle, "peer-seekable-open-range-reader", attrOpRangeReader,
		func(ctx context.Context) (peerAttempt[io.ReadCloser], error) {
			streamCtx, cancel := context.WithCancel(ctx)

			recv, outcome, err := openPeerSeekableStream(streamCtx, s.client, &orchestrator.ReadAtBuildSeekableRequest{
				BuildId: s.buildID,
				Name:    s.name,
				Offset:  off,
				Length:  length,
			}, s.state)
			if err != nil {
				cancel()
				logger.L().Warn(ctx, "failed to open range reader from peer", logger.WithBuildID(s.buildID), zap.Int64("off", off), zap.Int64("length", length), zap.Error(err))

				return peerAttempt[io.ReadCloser]{}, err
			}
			if outcome != served {
				cancel()

				return peerAttempt[io.ReadCloser]{result: outcome}, nil
			}

			return peerAttempt[io.ReadCloser]{
				value:  newPeerStreamReader(recv, cancel),
				result: served,
			}, nil
		})
	if hit {
		return rc, err
	}

	base, err := s.getBase(ctx, frameTable.CompressionType())
	if err != nil {
		return nil, err
	}

	return base.OpenRangeReader(ctx, off, length, frameTable)
}

func (s *peerSeekable) StoreFile(context.Context, string, ...storage.PutOption) (*storage.FrameTable, [32]byte, error) {
	// peerSeekable only exists when routingProvider routed this buildID to an
	// active peer at open time, i.e. the file is being P2P-served (the peer
	// owns the upload). Asking the local orchestrator to upload it is a
	// contradiction. The write path uses bare persistence (Upload.store) and
	// does not flow through routingProvider, so this is unreachable today;
	// returning an error keeps the contradiction explicit rather than letting
	// a future caller silently upload to the wrong path.
	return nil, [32]byte{}, fmt.Errorf("peerSeekable: StoreFile not supported (build %s is P2P-served; writes must use the base provider directly)", s.buildID)
}

// openPeerSeekableStream opens a ReadAtBuildSeekable stream, checks peer availability,
// and returns a recv function that yields data chunks starting with the first message's data.
// Mid-stream non-Served signals abort via storage.ErrPeerAborted; the caller reopens via base.
// The passed context HAS to be canceled by the caller when done with the stream to avoid leaks.
func openPeerSeekableStream(
	ctx context.Context,
	client orchestrator.ChunkServiceClient,
	req *orchestrator.ReadAtBuildSeekableRequest,
	state *peerState,
) (func() ([]byte, error), result, error) {
	stream, err := client.ReadAtBuildSeekable(ctx, req)
	if err != nil {
		return nil, 0, fmt.Errorf("open seekable stream: %w", err)
	}

	msg, err := stream.Recv()
	if err != nil {
		return nil, 0, fmt.Errorf("recv first seekable message: %w", err)
	}

	if outcome := checkPeerAvailability(ctx, msg.GetAvailability(), state, req.GetName()); outcome != served {
		return nil, outcome, nil
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

		if checkPeerAvailability(ctx, m.GetAvailability(), state, req.GetName()) != served {
			return nil, storage.ErrPeerAborted
		}

		return m.GetData(), nil
	}, served, nil
}
