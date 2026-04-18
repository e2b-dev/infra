package nbd

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

var (
	nbdReadDuration = utils.Must(meter.Int64Histogram("orchestrator.nbd.dispatch.read.duration",
		metric.WithDescription("Duration of NBD dispatch handler ReadAt calls to the backend."),
		metric.WithUnit("ms"),
	))
	nbdReadConncurent = utils.Must(meter.Int64UpDownCounter("orchestrator.nbd.dispatch.read.concurrent",
		metric.WithDescription("Number of NBD read requests currently waiting for a response. A sustained high value indicates reads stuck in kernel I/O."),
		metric.WithUnit("{read}"),
	))
	nbdReadSuccess   = metric.WithAttributeSet(attribute.NewSet(attribute.String("result", "success")))
	nbdReadFailure   = metric.WithAttributeSet(attribute.NewSet(attribute.String("result", "failure")))
	nbdReadCancelled = metric.WithAttributeSet(attribute.NewSet(attribute.String("result", "cancelled")))
)

var ErrShuttingDown = errors.New("shutting down. Cannot serve any new requests")

// dispatchPayloadPool reuses byte buffers across NBD read responses and write
// payloads. Buffers are allocated on demand per request, so the pool simply
// recycles them to avoid the per-request allocation overhead the pre-zero-copy
// dispatch had.
var dispatchPayloadPool = sync.Pool{
	New: func() any {
		b := make([]byte, 0)

		return &b
	},
}

func getPayloadBuf(length int) *[]byte {
	bp := dispatchPayloadPool.Get().(*[]byte)
	if cap(*bp) < length {
		*bp = make([]byte, length)
	} else {
		*bp = (*bp)[:length]
	}

	return bp
}

func putPayloadBuf(bp *[]byte) {
	*bp = (*bp)[:0]
	dispatchPayloadPool.Put(bp)
}

type Provider interface {
	ReadSlices(ctx context.Context, off, length int64, dest [][]byte) ([][]byte, error)
	WriteFrom(r io.Reader, off, length int64) error
	Size(ctx context.Context) (int64, error)
	BlockSize() int64
}

const (
	// Only needs to hold NBD request headers (28 bytes each).
	dispatchBufferSize = 4 * 1024
	// https://sourceforge.net/p/nbd/mailman/message/35081223/
	// 32MB is the maximum buffer size for a single request that should be universally supported.
	dispatchMaxWriteBufferSize = 32 * 1024 * 1024
)

// NBD Commands
const (
	NBDCmdRead       = 0
	NBDCmdWrite      = 1
	NBDCmdDisconnect = 2
	NBDCmdFlush      = 3
	NBDCmdTrim       = 4
)

const (
	NBDRequestMagic  = 0x25609513
	NBDResponseMagic = 0x67446698
)

// NBD Request packet
type Request struct {
	Magic  uint32
	Type   uint32
	Handle uint64
	From   uint64
	Length uint32
}

// NBD Response packet
type Response struct {
	Magic  uint32
	Error  uint32
	Handle uint64
}

type Dispatch struct {
	fp             io.ReadWriter
	responseHeader []byte
	writeLock      sync.Mutex
	prov           Provider
	// provName is the concrete backend type name, cached at construction so
	// error logs can identify which storage layer failed without reflection
	// on every call.
	provName         string
	pendingResponses sync.WaitGroup
	shuttingDown     bool
	shuttingDownLock sync.Mutex
	fatal            chan error
}

func NewDispatch(fp io.ReadWriter, prov Provider) *Dispatch {
	d := &Dispatch{
		responseHeader: make([]byte, 16),
		fp:             fp,
		prov:           prov,
		provName:       fmt.Sprintf("%T", prov),
		fatal:          make(chan error, 1),
	}

	binary.BigEndian.PutUint32(d.responseHeader, NBDResponseMagic)

	return d
}

func (d *Dispatch) Drain() {
	d.shuttingDownLock.Lock()
	d.shuttingDown = true
	defer d.shuttingDownLock.Unlock()

	// Wait for any pending responses
	d.pendingResponses.Wait()
}

/**
 * Write a response...
 *
 */
func (d *Dispatch) writeResponse(respError uint32, respHandle uint64, chunk []byte) error {
	d.writeLock.Lock()
	defer d.writeLock.Unlock()

	binary.BigEndian.PutUint32(d.responseHeader[4:], respError)
	binary.BigEndian.PutUint64(d.responseHeader[8:], respHandle)

	_, err := d.fp.Write(d.responseHeader)
	if err != nil {
		return err
	}
	if len(chunk) > 0 {
		_, err = d.fp.Write(chunk)
		if err != nil {
			return err
		}
	}

	return nil
}

/**
 * This dispatches incoming NBD requests to the provider, dispatching reads
 * and writes asynchronously so a single slow backend operation cannot stall
 * pipelined requests on the same NBD connection.
 *
 */
func (d *Dispatch) Handle(ctx context.Context) error {
	buffer := make([]byte, dispatchBufferSize)
	wp := 0

	request := Request{}

	for {
		n, err := d.fp.Read(buffer[wp:])
		if err != nil {
			return err
		}
		wp += n

		// Now go through processing complete packets
		rp := 0
		for {
			// Check if there is a fatal error from an async read/write to return
			select {
			case err := <-d.fatal:
				return err
			case <-ctx.Done():
				return ctx.Err()
			default:
			}

			// Make sure we have a complete header
			if wp-rp < 28 {
				break // Try again when we have more data...
			}

			// We can read the neader...

			header := buffer[rp : rp+28]
			request.Magic = binary.BigEndian.Uint32(header)
			request.Type = binary.BigEndian.Uint32(header[4:8])
			request.Handle = binary.BigEndian.Uint64(header[8:16])
			request.From = binary.BigEndian.Uint64(header[16:24])
			request.Length = binary.BigEndian.Uint32(header[24:28])

			if request.Magic != NBDRequestMagic {
				return fmt.Errorf("received invalid MAGIC")
			}

			switch request.Type {
			case NBDCmdDisconnect:
				return nil // All done
			case NBDCmdFlush:
				return fmt.Errorf("not supported: Flush")
			case NBDCmdRead:
				rp += 28
				err := d.cmdRead(ctx, request.Handle, request.From, request.Length)
				if err != nil {
					return err
				}
			case NBDCmdWrite:
				rp += 28

				if request.Length > dispatchMaxWriteBufferSize {
					return fmt.Errorf("nbd write request length %d exceeds maximum %d", request.Length, dispatchMaxWriteBufferSize)
				}

				// Stage the payload into a pooled buffer so the dispatch loop
				// (and the cache write lock inside WriteFrom) is not held
				// during the socket read. The async cmdWrite below then
				// hands the in-memory buffer to the backend.
				payloadBuf := getPayloadBuf(int(request.Length))
				payload := *payloadBuf

				leftover := min(wp-rp, int(request.Length))
				copy(payload, buffer[rp:rp+leftover])
				rp += leftover

				if leftover < int(request.Length) {
					if _, readErr := io.ReadFull(d.fp, payload[leftover:]); readErr != nil {
						putPayloadBuf(payloadBuf)

						return fmt.Errorf("nbd write read error: %w", readErr)
					}
				}

				err := d.cmdWrite(ctx, request.Handle, request.From, payloadBuf)
				if err != nil {
					return err
				}
			case NBDCmdTrim:
				rp += 28
				err := d.cmdTrim(request.Handle, request.From, request.Length)
				if err != nil {
					return err
				}
			default:
				return fmt.Errorf("nbd not implemented %d", request.Type)
			}
		}
		// Now we need to move any partial to the start
		if rp != 0 && rp != wp {
			copy(buffer, buffer[rp:wp])
		}
		wp -= rp
	}
}

// fatalIfFirst publishes the first non-recoverable dispatch error so the
// Handle loop can return it. Subsequent errors are logged but not propagated:
// once the first failure is queued the connection is being torn down anyway,
// and writing additional errors would block on the unbuffered channel.
func (d *Dispatch) fatalIfFirst(ctx context.Context, op string, handle uint64, from uint64, length uint32, err error) {
	select {
	case d.fatal <- err:
	default:
		logger.L().Error(ctx, "nbd dispatch error",
			zap.Error(err),
			zap.String("nbd_op", op),
			zap.String("nbd_provider", d.provName),
			zap.Uint64("nbd_handle", handle),
			zap.Uint64("nbd_offset", from),
			zap.Uint32("nbd_length", length),
		)
	}
}

func (d *Dispatch) cmdRead(ctx context.Context, cmdHandle uint64, cmdFrom uint64, cmdLength uint32) error {
	d.shuttingDownLock.Lock()
	if d.shuttingDown {
		d.shuttingDownLock.Unlock()

		return ErrShuttingDown
	}

	d.pendingResponses.Add(1)
	d.shuttingDownLock.Unlock()

	go func() {
		defer d.pendingResponses.Done()

		err := d.performRead(ctx, cmdHandle, cmdFrom, cmdLength)
		if err != nil {
			d.fatalIfFirst(ctx, "read", cmdHandle, cmdFrom, cmdLength, err)
		}
	}()

	return nil
}

// performRead executes a single NBD read against the backend and writes the
// response to the socket. Backend failures are reported per-request via the
// NBD response error byte; only socket-write failures are returned, since
// those mean the connection is unusable.
func (d *Dispatch) performRead(ctx context.Context, handle uint64, from uint64, length uint32) error {
	errchan := make(chan error, 1)

	var slices [][]byte

	nbdReadConncurent.Add(ctx, 1)
	defer nbdReadConncurent.Add(ctx, -1)

	go func() {
		start := time.Now()
		var err error

		slices, err = d.prov.ReadSlices(ctx, int64(from), int64(length), nil)

		attrs := nbdReadSuccess
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				attrs = nbdReadCancelled
			} else {
				attrs = nbdReadFailure
			}
		}

		nbdReadDuration.Record(ctx, time.Since(start).Milliseconds(), attrs)
		errchan <- err
	}()

	var readErr error
	select {
	case <-ctx.Done():
		readErr = ctx.Err()
	case readErr = <-errchan:
	}

	if readErr != nil {
		logger.L().Error(ctx, "nbd backend read failed",
			zap.Error(readErr),
			zap.String("nbd_op", "read"),
			zap.String("nbd_provider", d.provName),
			zap.Uint64("nbd_handle", handle),
			zap.Uint64("nbd_offset", from),
			zap.Uint32("nbd_length", length),
		)

		return d.writeResponse(1, handle, nil)
	}

	// Copy mmap slices into a pooled buffer before writing to the socket.
	// Mirrors the original ReadAt behavior so concurrent mutations of the
	// underlying mmap (e.g. another connection's write, snapshot/diff
	// export, chunker eviction) cannot corrupt an in-flight response.
	bufp := getPayloadBuf(int(length))
	defer putPayloadBuf(bufp)
	buf := *bufp

	off := 0
	for _, s := range slices {
		off += copy(buf[off:], s)
	}

	return d.writeResponse(0, handle, buf)
}

func (d *Dispatch) cmdWrite(ctx context.Context, cmdHandle uint64, cmdFrom uint64, payloadBuf *[]byte) error {
	d.shuttingDownLock.Lock()
	if d.shuttingDown {
		d.shuttingDownLock.Unlock()

		putPayloadBuf(payloadBuf)

		return ErrShuttingDown
	}

	d.pendingResponses.Add(1)
	d.shuttingDownLock.Unlock()

	go func() {
		defer d.pendingResponses.Done()
		defer putPayloadBuf(payloadBuf)

		err := d.performWrite(ctx, cmdHandle, cmdFrom, *payloadBuf)
		if err != nil {
			d.fatalIfFirst(ctx, "write", cmdHandle, cmdFrom, uint32(len(*payloadBuf)), err)
		}
	}()

	return nil
}

func (d *Dispatch) performWrite(ctx context.Context, handle uint64, from uint64, payload []byte) error {
	errchan := make(chan error, 1)

	go func() {
		errchan <- d.prov.WriteFrom(bytes.NewReader(payload), int64(from), int64(len(payload)))
	}()

	var writeErr error
	select {
	case <-ctx.Done():
		writeErr = ctx.Err()
	case writeErr = <-errchan:
	}

	if writeErr != nil {
		logger.L().Error(ctx, "nbd backend write failed",
			zap.Error(writeErr),
			zap.String("nbd_op", "write"),
			zap.String("nbd_provider", d.provName),
			zap.Uint64("nbd_handle", handle),
			zap.Uint64("nbd_offset", from),
			zap.Int("nbd_length", len(payload)),
		)

		return d.writeResponse(1, handle, nil)
	}

	return d.writeResponse(0, handle, nil)
}

/**
 * cmdTrim
 *
 */
func (d *Dispatch) cmdTrim(handle uint64, _ uint64, _ uint32) error {
	// TODO: Ask the provider
	/*
		e := d.prov.Trim(from, length)
		if e != storage.StorageError_SUCCESS {
			err := d.writeResponse(1, handle, []byte{})
			if err != nil {
				return err
			}
		} else {
	*/
	err := d.writeResponse(0, handle, []byte{})
	if err != nil {
		return err
	}
	//	}
	return nil
}
