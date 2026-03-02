package server

import (
	"io"

	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

// chunkSendSize is the maximum number of bytes sent in a single gRPC stream message.
const chunkSendSize = storage.MemoryChunkSize

// chunkWriter buffers data and sends it to a gRPC stream in chunkSendSize-bounded messages.
type chunkWriter struct {
	stream orchestrator.ChunkService_GetBuildFileServer
	buf    []byte
}

func newChunkWriter(stream orchestrator.ChunkService_GetBuildFileServer) *chunkWriter {
	return &chunkWriter{stream: stream}
}

func (w *chunkWriter) Write(p []byte) (int, error) {
	total := 0

	for len(p) > 0 {
		space := chunkSendSize - len(w.buf)
		take := min(len(p), space)
		w.buf = append(w.buf, p[:take]...)
		p = p[take:]
		total += take

		if len(w.buf) == chunkSendSize {
			if err := w.flush(); err != nil {
				return total, err
			}
		}
	}

	return total, nil
}

func (w *chunkWriter) flush() error {
	if len(w.buf) == 0 {
		return nil
	}

	chunk := w.buf
	w.buf = nil

	return w.stream.Send(&orchestrator.GetBuildFileResponse{Data: chunk})
}

// copyToStream pipes r into chunkSendSize-bounded gRPC stream messages.
func copyToStream(span trace.Span, r io.Reader, stream orchestrator.ChunkService_GetBuildFileServer) error {
	w := newChunkWriter(stream)
	if _, err := io.Copy(w, r); err != nil {
		span.RecordError(err)
		return status.Errorf(codes.Internal, "failed to stream data: %v", err)
	}

	return w.flush()
}
