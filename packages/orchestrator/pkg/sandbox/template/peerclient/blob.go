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

var _ storage.Blob = (*peerBlob)(nil)

// peerBlob reads from the peer first; on fallthrough, opens base lazily.
// The base path is fixed at construction (blobs are not compressed).
type peerBlob struct {
	peerHandle

	openBase func(ctx context.Context) (storage.Blob, error)

	mu     sync.Mutex
	base   storage.Blob
	loaded bool
}

func (b *peerBlob) getBase(ctx context.Context) (storage.Blob, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.loaded {
		return b.base, nil
	}

	base, err := b.openBase(ctx)
	if err != nil {
		return nil, err
	}

	b.base = base
	b.loaded = true

	return base, nil
}

func (b *peerBlob) WriteTo(ctx context.Context, dst io.Writer) (int64, error) {
	res, err := tryPeer(ctx, &b.peerHandle, "peer-blob-write-to", attrOpWriteTo,
		func(ctx context.Context) (peerAttempt[int64], error) {
			streamCtx, cancel := context.WithCancel(ctx)

			recv, err := openPeerBlobStream(streamCtx, b.client, &orchestrator.GetBuildBlobRequest{
				BuildId:  b.buildID,
				FileName: b.fileName,
			}, b.uploaded)
			if err != nil {
				cancel()
				logger.L().Warn(ctx, "failed to open peer blob stream", logger.WithBuildID(b.buildID), zap.String("file_name", b.fileName), zap.Error(err))

				return peerAttempt[int64]{}, nil
			}

			reader := newPeerStreamReader(recv, cancel)
			defer reader.Close()

			n, err := io.Copy(dst, reader)
			if err != nil {
				return peerAttempt[int64]{value: n, bytes: n, hit: true},
					fmt.Errorf("failed to stream file %q from peer: %w", b.fileName, err)
			}

			return peerAttempt[int64]{value: n, bytes: n, hit: true}, nil
		})
	if res.hit {
		return res.value, err
	}

	base, err := b.getBase(ctx)
	if err != nil {
		return 0, err
	}

	return base.WriteTo(ctx, dst)
}

func (b *peerBlob) Exists(ctx context.Context) (bool, error) {
	res, err := tryPeer(ctx, &b.peerHandle, "peer-blob-exists", attrOpExists,
		func(ctx context.Context) (peerAttempt[bool], error) {
			resp, err := b.client.GetBuildFileExists(ctx, &orchestrator.GetBuildFileExistsRequest{
				BuildId:  b.buildID,
				FileName: b.fileName,
			})
			if err == nil && checkPeerAvailability(resp.GetAvailability(), b.uploaded) {
				return peerAttempt[bool]{value: true, hit: true}, nil
			}

			if err != nil {
				logger.L().Warn(ctx, "failed to check build file exists from peer", logger.WithBuildID(b.buildID), zap.String("file_name", b.fileName), zap.Error(err))
			}

			return peerAttempt[bool]{}, nil
		})
	if res.hit {
		return res.value, err
	}

	base, err := b.getBase(ctx)
	if err != nil {
		return false, err
	}

	return base.Exists(ctx)
}

func (b *peerBlob) Put(ctx context.Context, data []byte, opts ...storage.PutOption) error {
	// Writes always go to the base provider (GCS/S3); the peer is read-only.
	fallback, err := b.getBase(ctx)
	if err != nil {
		return err
	}

	return fallback.Put(ctx, data, opts...)
}

// openPeerBlobStream opens a GetBuildBlob stream, checks peer availability,
// and returns a recv function that yields data chunks starting with the first message's data.
// The passed context HAS to be canceled by the caller when done with the stream to avoid leaks.
func openPeerBlobStream(
	ctx context.Context,
	client orchestrator.ChunkServiceClient,
	req *orchestrator.GetBuildBlobRequest,
	uploaded *atomic.Bool,
) (func() ([]byte, error), error) {
	stream, err := client.GetBuildBlob(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("open blob stream: %w", err)
	}

	msg, err := stream.Recv()
	if err != nil {
		return nil, fmt.Errorf("recv first blob message: %w", err)
	}

	if !checkPeerAvailability(msg.GetAvailability(), uploaded) {
		return nil, errors.New("peer not available for blob stream")
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
