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
	provName string
	// readBuf is a reusable buffer used to copy read response data out of
	// mmap slices before writing it to the NBD socket. Decoupling the
	// socket write from the mmap means the underlying pages may be modified
	// by concurrent writes (across NBD connections, snapshot/diff export,
	// etc.) without corrupting in-flight responses.
	readBuf          []byte
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

// copySlicesToReadBuf assembles slices into the reusable read buffer so the
// caller can hand a stable []byte to writeResponse. Returning a copy (instead
// of writing the mmap slices straight to the socket) guarantees the bytes
// sent on the wire match a single point-in-time snapshot of the backend, even
// if the underlying mmap pages are mutated concurrently while we're writing.
func (d *Dispatch) copySlicesToReadBuf(length int, slices [][]byte) []byte {
	if cap(d.readBuf) < length {
		d.readBuf = make([]byte, length)
	}
	buf := d.readBuf[:length]

	off := 0
	for _, s := range slices {
		off += copy(buf[off:], s)
	}

	return buf
}

/**
 * This dispatches incoming NBD requests sequentially to the provider.
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

				// Feed leftover buffer bytes + socket into the provider, which
				// reads directly into the mmap page — zero intermediate buffer.
				leftover := min(wp-rp, int(request.Length))
				reader := io.MultiReader(bytes.NewReader(buffer[rp:rp+leftover]), d.fp)
				rp += leftover

				err := d.prov.WriteFrom(reader, int64(request.From), int64(request.Length))
				if err != nil {
					// Per-request backend failure: signal it to the NBD client
					// via the response error byte and keep the dispatch loop
					// alive. Only writeResponse errors (dead NBD socket)
					// escalate by being returned from Handle.
					logger.L().Error(ctx, "nbd backend write failed",
						zap.Error(err),
						zap.String("nbd_op", "write"),
						zap.String("nbd_provider", d.provName),
						zap.Uint64("nbd_handle", request.Handle),
						zap.Uint64("nbd_offset", request.From),
						zap.Uint32("nbd_length", request.Length),
					)

					if respErr := d.writeResponse(1, request.Handle, nil); respErr != nil {
						return respErr
					}

					continue
				}

				err = d.writeResponse(0, request.Handle, nil)
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

func (d *Dispatch) cmdRead(ctx context.Context, cmdHandle uint64, cmdFrom uint64, cmdLength uint32) error {
	// Process synchronously so we hold the dispatch loop until the read
	// response has been copied out of the mmap slices and written to the
	// socket. Concurrency across requests is preserved by running multiple
	// NBD socket connections per device (FlagCanMulticonn).
	// buffered to avoid goroutine leak
	errchan := make(chan error, 1)

	var slices [][]byte

	nbdReadConncurent.Add(ctx, 1)
	defer nbdReadConncurent.Add(ctx, -1)

	go func() {
		start := time.Now()
		var err error

		slices, err = d.prov.ReadSlices(ctx, int64(cmdFrom), int64(cmdLength), nil)

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

	// Wait until either the ReadSlices completed, or our context is cancelled...
	var readErr error
	select {
	case <-ctx.Done():
		readErr = ctx.Err()
	case readErr = <-errchan:
	}

	if readErr != nil {
		// Per-request backend failure: signal it to the NBD client via the
		// response error byte and keep the dispatch loop alive. Only
		// writeResponse errors (dead NBD socket) escalate by being returned
		// from Handle.
		logger.L().Error(ctx, "nbd backend read failed",
			zap.Error(readErr),
			zap.String("nbd_op", "read"),
			zap.String("nbd_provider", d.provName),
			zap.Uint64("nbd_handle", cmdHandle),
			zap.Uint64("nbd_offset", cmdFrom),
			zap.Uint32("nbd_length", cmdLength),
		)

		return d.writeResponse(1, cmdHandle, nil)
	}

	// Copy mmap slices into a stable buffer before writing to the socket.
	// Mirrors the original ReadAt behavior so concurrent mutations of the
	// underlying mmap (e.g. another connection's write, snapshot/diff
	// export, chunker eviction) cannot corrupt an in-flight response.
	return d.writeResponse(0, cmdHandle, d.copySlicesToReadBuf(int(cmdLength), slices))
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
