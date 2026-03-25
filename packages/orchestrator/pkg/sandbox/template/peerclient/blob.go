package peerclient

import (
	"context"
	"fmt"
	"io"
	"sync/atomic"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

var _ storage.Blob = (*peerBlob)(nil)

type peerBlob struct {
	peerHandle[storage.Blob]
}

func (b *peerBlob) WriteTo(ctx context.Context, dst io.Writer) (int64, error) {
	return withPeerFallback(ctx, &b.peerHandle, "peer-blob-write-to", attrOpWriteTo,
		func(ctx context.Context) (peerAttempt[int64], error) {
			recv, err := openPeerBlobStream(ctx, b.client, &orchestrator.GetBuildBlobRequest{
				BuildId:  b.buildID,
				FileName: b.fileName,
			}, b.uploaded)
			if err != nil {
				logger.L().Warn(ctx, "failed to open peer blob stream", logger.WithBuildID(b.buildID), zap.String("file_name", b.fileName), zap.Error(err))

				return peerAttempt[int64]{}, nil
			}

			n, err := io.Copy(dst, newPeerStreamReader(recv, func() {}))
			if err != nil {
				return peerAttempt[int64]{value: n, bytes: n, hit: true},
					fmt.Errorf("failed to stream file %q from peer: %w", b.fileName, err)
			}

			return peerAttempt[int64]{value: n, bytes: n, hit: true}, nil
		},
		func(ctx context.Context, base storage.Blob) (int64, error) {
			return base.WriteTo(ctx, dst)
		},
	)
}

func (b *peerBlob) Exists(ctx context.Context) (bool, error) {
	return withPeerFallback(ctx, &b.peerHandle, "peer-blob-exists", attrOpExists,
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
		},
		func(ctx context.Context, base storage.Blob) (bool, error) {
			return base.Exists(ctx)
		},
	)
}

func (b *peerBlob) Put(ctx context.Context, data []byte) error {
	// Writes always go to the base provider (GCS/S3); the peer is read-only.
	fallback, err := b.getOrOpenBase(ctx)
	if err != nil {
		return err
	}

	return fallback.Put(ctx, data)
}

// openPeerBlobStream opens a GetBuildBlob stream, checks peer availability,
// and returns a recv function that yields data chunks starting with the first message's data.
func openPeerBlobStream(
	ctx context.Context,
	client orchestrator.ChunkServiceClient,
	req *orchestrator.GetBuildBlobRequest,
	uploaded *atomic.Pointer[UploadedHeaders],
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
		return nil, fmt.Errorf("peer not available for blob stream")
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
