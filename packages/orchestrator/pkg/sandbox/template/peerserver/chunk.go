//go:build linux

package peerserver

// sendChunkSize bounds the payload of a single Sender.Send call. gRPC applies
// its receive limit to the encoded message, so the payload has to leave room
// for the protobuf framing. Deliberately not storage.MemoryChunkSize, which
// equals the default limit exactly and so overflows once framed.
const sendChunkSize = 1 << 20 // 1 MiB

// sendChunked forwards data to sender in sendChunkSize-bounded calls. An empty
// payload still produces one Send: a stream carrying no messages reads at the
// receiver as a peer miss rather than as an empty body.
func sendChunked(sender Sender, data []byte) error {
	for {
		take := min(len(data), sendChunkSize)
		if err := sender.Send(data[:take]); err != nil {
			return err
		}

		data = data[take:]
		if len(data) == 0 {
			return nil
		}
	}
}

// chunkWriter adapts sendChunked to io.Writer for sources that stream from disk
// rather than from a buffer already in memory. Buffered writes are forwarded
// once they reach sendChunkSize; the caller must flush the remainder.
type chunkWriter struct {
	sender Sender
	buf    []byte
}

func (w *chunkWriter) Write(p []byte) (int, error) {
	total := 0

	for len(p) > 0 {
		space := sendChunkSize - len(w.buf)
		take := min(len(p), space)
		w.buf = append(w.buf, p[:take]...)
		p = p[take:]
		total += take

		if len(w.buf) == sendChunkSize {
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

	return w.sender.Send(chunk)
}
