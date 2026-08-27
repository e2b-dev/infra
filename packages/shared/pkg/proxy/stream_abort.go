package proxy

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

const (
	connectStreamContentType = "application/connect+"

	connectFlagEndStream = 0b00000010

	envelopePrefixLen = 5

	// The peer went away rather than refused the call: the one status clients retry on.
	connectCodeUnavailable = "unavailable"
)

// serveStream runs proxy, converting an aborted RPC stream into the protocol's
// own terminal error frame. ReverseProxy answers a mid-body read error with
// panic(http.ErrAbortHandler), so the body stops with no status and the client
// can only report a framing error. A framing that cannot carry an error is
// re-panicked: ending a plain body cleanly would hide the truncation.
func serveStream(w http.ResponseWriter, r *http.Request, proxy http.Handler, message string) {
	sw := &streamWriter{ResponseWriter: w}

	defer func() {
		recovered := recover()
		if recovered == nil {
			return
		}

		err, ok := recovered.(error)
		if !ok || !errors.Is(err, http.ErrAbortHandler) || !sw.endStream(message) {
			panic(recovered)
		}
	}()

	proxy.ServeHTTP(sw, r)
}

// streamWriter follows the envelope framing so an aborted response can be closed
// off at a frame boundary.
type streamWriter struct {
	http.ResponseWriter

	// nil when the response framing cannot report an error mid-body.
	endFrame  func(message string) []byte
	envelopes envelopeTracker
}

// Unwrap lets http.ResponseController reach Flush and Hijack. Without it a
// protocol upgrade through the proxy fails.
func (w *streamWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func (w *streamWriter) WriteHeader(status int) {
	if status == http.StatusOK {
		w.endFrame = endFrameFor(w.Header().Get("Content-Type"))
	}

	w.ResponseWriter.WriteHeader(status)
}

func (w *streamWriter) Write(b []byte) (int, error) {
	n, err := w.ResponseWriter.Write(b)
	if w.endFrame != nil {
		w.envelopes.advance(b[:n])
	}

	return n, err
}

// endStream reports whether it could close the response off. A half-written
// envelope cannot be completed without inventing message bytes.
func (w *streamWriter) endStream(message string) bool {
	if w.endFrame == nil || !w.envelopes.atBoundary() {
		return false
	}

	frame := w.endFrame(message)
	if frame == nil {
		return false
	}

	_, err := w.ResponseWriter.Write(frame)

	return err == nil
}

// endFrameFor returns nil when the framing cannot report an error once the body
// has started.
func endFrameFor(contentType string) func(message string) []byte {
	if strings.HasPrefix(contentType, connectStreamContentType) {
		return connectEndStreamFrame
	}

	return nil
}

func connectEndStreamFrame(message string) []byte {
	body, err := json.Marshal(connectEndStream{
		Error: &connectEndStreamError{Code: connectCodeUnavailable, Message: message},
	})
	if err != nil {
		return nil
	}

	return envelopeFrame(connectFlagEndStream, body)
}

func envelopeFrame(flag byte, body []byte) []byte {
	frame := make([]byte, envelopePrefixLen, envelopePrefixLen+len(body))
	frame[0] = flag
	binary.BigEndian.PutUint32(frame[1:], uint32(len(body)))

	return append(frame, body...)
}

type connectEndStream struct {
	Error *connectEndStreamError `json:"error,omitempty"`
}

type connectEndStreamError struct {
	Code    string `json:"code"`
	Message string `json:"message,omitempty"`
}

// envelopeTracker holds the position within the current 5-byte-prefixed frame.
type envelopeTracker struct {
	prefix    [envelopePrefixLen]byte
	prefixLen int
	remaining int64
}

func (t *envelopeTracker) advance(b []byte) {
	for {
		// A frame ends the moment its payload is consumed, which for a
		// zero-length payload is as soon as the prefix is read.
		if t.prefixLen == envelopePrefixLen && t.remaining == 0 {
			t.prefixLen = 0
		}

		if len(b) == 0 {
			return
		}

		if t.prefixLen < envelopePrefixLen {
			read := copy(t.prefix[t.prefixLen:], b)
			t.prefixLen += read
			b = b[read:]

			if t.prefixLen == envelopePrefixLen {
				t.remaining = int64(binary.BigEndian.Uint32(t.prefix[1:]))
			}

			continue
		}

		read := min(int64(len(b)), t.remaining)
		t.remaining -= read
		b = b[read:]
	}
}

// atBoundary reports whether a further frame can be appended.
func (t *envelopeTracker) atBoundary() bool {
	return t.prefixLen == 0 && t.remaining == 0
}
