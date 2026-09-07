//go:build linux

package peerserver

import (
	"bytes"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

// grpcDefaultMaxRecvMsgSize is gRPC's default inbound message limit, restated
// because the library does not export it.
const grpcDefaultMaxRecvMsgSize = 4 * 1024 * 1024

// The limit covers the encoded message, so a chunk has to fit once the field
// tag and length prefix are added. Staying inside the default is what lets a
// receiver read the stream without configuring anything.
func TestSendChunkSize_EncodesUnderGRPCDefault(t *testing.T) {
	t.Parallel()

	encoded := proto.Size(&orchestrator.GetBuildBlobResponse{Data: make([]byte, sendChunkSize)})

	assert.Greater(t, encoded, sendChunkSize,
		"framing must add to the payload; if it does not, this test no longer proves anything")
	assert.LessOrEqual(t, encoded, grpcDefaultMaxRecvMsgSize)
}

// storage.MemoryChunkSize equals the receive limit exactly, so a chunk that
// size overflows once encoded. That is why the send bound is its own constant,
// and this fails if the two are ever tied together.
func TestMemoryChunkSize_OverflowsGRPCDefault(t *testing.T) {
	t.Parallel()

	require.Equal(t, grpcDefaultMaxRecvMsgSize, storage.MemoryChunkSize,
		"the storage chunk size is expected to equal the transport limit exactly; that collision is the hazard being pinned")

	encoded := proto.Size(&orchestrator.GetBuildBlobResponse{Data: make([]byte, storage.MemoryChunkSize)})

	assert.Greater(t, encoded, grpcDefaultMaxRecvMsgSize)
}

func TestSendChunked_BoundsEverySend(t *testing.T) {
	t.Parallel()

	// The tail is derived from the bound rather than fixed, so the expectation
	// holds whatever sendChunkSize is set to.
	const tail = sendChunkSize / 2

	payload := bytes.Repeat([]byte("x"), 2*sendChunkSize+tail)

	sender := &collectSender{}
	require.NoError(t, sendChunked(sender, payload))

	assert.Equal(t, []int{sendChunkSize, sendChunkSize, tail}, sender.sends)
	assert.Equal(t, payload, sender.data)
}

func TestSendChunked_ExactMultipleDoesNotSendEmptyTail(t *testing.T) {
	t.Parallel()

	payload := bytes.Repeat([]byte("x"), 2*sendChunkSize)

	sender := &collectSender{}
	require.NoError(t, sendChunked(sender, payload))

	assert.Equal(t, []int{sendChunkSize, sendChunkSize}, sender.sends)
	assert.Equal(t, payload, sender.data)
}

func TestSendChunked_SmallPayloadIsOneSend(t *testing.T) {
	t.Parallel()

	sender := &collectSender{}
	require.NoError(t, sendChunked(sender, []byte("small")))

	assert.Equal(t, []int{5}, sender.sends)
	assert.Equal(t, []byte("small"), sender.data)
}

// A stream with no messages is indistinguishable at the receiver from one that
// failed, and is read as a peer miss. An empty payload therefore has to occupy
// a message of its own.
func TestSendChunked_EmptyPayloadStillSends(t *testing.T) {
	t.Parallel()

	sender := &collectSender{}
	require.NoError(t, sendChunked(sender, nil))

	assert.Equal(t, []int{0}, sender.sends)
}

type failingSender struct {
	afterCalls int
	calls      int
}

var errSendFailed = errors.New("send failed")

func (s *failingSender) Send([]byte) error {
	s.calls++
	if s.calls > s.afterCalls {
		return errSendFailed
	}

	return nil
}

func TestSendChunked_StopsOnSendError(t *testing.T) {
	t.Parallel()

	sender := &failingSender{afterCalls: 1}
	err := sendChunked(sender, bytes.Repeat([]byte("x"), 3*sendChunkSize))

	require.ErrorIs(t, err, errSendFailed)
	assert.Equal(t, 2, sender.calls, "must abandon the payload at the first failed send")
}

func TestChunkWriter_BoundsEverySend(t *testing.T) {
	t.Parallel()

	payload := bytes.Repeat([]byte("y"), 2*sendChunkSize+7)

	sender := &collectSender{}
	w := &chunkWriter{sender: sender}

	// Write in pieces that straddle the chunk boundary, so the buffering path
	// is what produces the bound rather than the caller's write sizes.
	for off := 0; off < len(payload); off += 7 * 1024 {
		n, err := w.Write(payload[off:min(off+7*1024, len(payload))])
		require.NoError(t, err)
		require.Positive(t, n)
	}
	require.NoError(t, w.flush())

	assert.Equal(t, []int{sendChunkSize, sendChunkSize, 7}, sender.sends)
	assert.Equal(t, payload, sender.data)
}
