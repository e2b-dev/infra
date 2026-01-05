package nbd

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

// NBDEvent represents a single NBD read/write with timing information.
type NBDEvent struct {
	Timestamp int64  `json:"ts"`   // Unix nanoseconds when the request started
	Duration  int64  `json:"dur"`  // Time to complete the request (nanoseconds)
	Offset    int64  `json:"off"`  // Offset in the device
	Length    int64  `json:"len"`  // Length of the request
	IsWrite   bool   `json:"wr"`   // True if write, false if read
}

var ErrShuttingDown = errors.New("shutting down. Cannot serve any new requests")

type Provider interface {
	storage.ReaderAtCtx
	io.WriterAt
	Size() (int64, error)
}

const (
	// TODO: Look into optimizing the buffer reads by increasing the buffer size by 28 bytes,
	// to account for a request that is 28 bytes of header + 4MB of data (this seems to be preferred kernel buffer size).
	dispatchBufferSize = 4 * 1024 * 1024
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
	fp               io.ReadWriter
	responseHeader   []byte
	writeLock        sync.Mutex
	prov             Provider
	pendingResponses sync.WaitGroup
	shuttingDown     bool
	shuttingDownLock sync.Mutex
	fatal            chan error

	// Tracing
	traceMu      sync.Mutex
	traceEnabled bool
	traceEvents  []NBDEvent
}

func NewDispatch(fp io.ReadWriter, prov Provider) *Dispatch {
	d := &Dispatch{
		responseHeader: make([]byte, 16),
		fp:             fp,
		prov:           prov,
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

// SetTraceEnabled enables or disables NBD tracing.
func (d *Dispatch) SetTraceEnabled(enabled bool) {
	d.traceMu.Lock()
	defer d.traceMu.Unlock()
	d.traceEnabled = enabled
	if enabled && d.traceEvents == nil {
		d.traceEvents = make([]NBDEvent, 0, 256)
	}
}

// GetTrace returns a copy of the NBD trace events.
func (d *Dispatch) GetTrace() []NBDEvent {
	d.traceMu.Lock()
	defer d.traceMu.Unlock()
	result := make([]NBDEvent, len(d.traceEvents))
	copy(result, d.traceEvents)
	return result
}

func (d *Dispatch) recordEvent(startTime time.Time, offset, length int64, isWrite bool) {
	d.traceMu.Lock()
	defer d.traceMu.Unlock()
	if !d.traceEnabled {
		return
	}
	d.traceEvents = append(d.traceEvents, NBDEvent{
		Timestamp: startTime.UnixNano(),
		Duration:  time.Since(startTime).Nanoseconds(),
		Offset:    offset,
		Length:    length,
		IsWrite:   isWrite,
	})
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

				data := make([]byte, request.Length)

				dataCopied := copy(data, buffer[rp:wp])

				rp += dataCopied

				// We need to wait for more data here, otherwise we will deadlock if the buffer is Xmb and the length is Xmb because of the header's extra 28 bytes needed.
				// At the same time we don't want to increase the default buffer size as the max would be 32mb which is too large for hundreds of sandbox connections.

				for dataCopied < int(request.Length) {
					n, err := d.fp.Read(data[dataCopied:])
					if err != nil {
						return fmt.Errorf("nbd write read error: %w", err)
					}

					dataCopied += n

					select {
					case err := <-d.fatal:
						return err
					case <-ctx.Done():
						return ctx.Err()
					default:
					}
				}

				err := d.cmdWrite(ctx, request.Handle, request.From, data)
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
	d.shuttingDownLock.Lock()
	if d.shuttingDown {
		d.shuttingDownLock.Unlock()

		return ErrShuttingDown
	}

	d.pendingResponses.Add(1)
	d.shuttingDownLock.Unlock()

	performRead := func(handle uint64, from uint64, length uint32) error {
		startTime := time.Now()

		// buffered to avoid goroutine leak
		errchan := make(chan error, 1)
		data := make([]byte, length)

		go func() {
			_, err := d.prov.ReadAt(ctx, data, int64(from))
			errchan <- err
		}()

		// Wait until either the ReadAt completed, or our context is cancelled...
		select {
		case <-ctx.Done():
			return d.writeResponse(1, handle, []byte{})
		case err := <-errchan:
			if err != nil {
				return d.writeResponse(1, handle, []byte{})
			}
		}

		// read was successful - write response and record event including response time
		err := d.writeResponse(0, handle, data)
		d.recordEvent(startTime, int64(from), int64(length), false)
		return err
	}

	go func() {
		err := performRead(cmdHandle, cmdFrom, cmdLength)
		if err != nil {
			select {
			case d.fatal <- err:
			default:
				logger.L().Error(ctx, "nbd error cmd read", zap.Error(err))
			}
		}
		d.pendingResponses.Done()
	}()

	return nil
}

func (d *Dispatch) cmdWrite(ctx context.Context, cmdHandle uint64, cmdFrom uint64, cmdData []byte) error {
	d.shuttingDownLock.Lock()
	if d.shuttingDown {
		d.shuttingDownLock.Unlock()

		return ErrShuttingDown
	}

	d.pendingResponses.Add(1)
	d.shuttingDownLock.Unlock()

	performWrite := func(handle uint64, from uint64, data []byte) error {
		startTime := time.Now()

		// buffered to avoid goroutine leak
		errchan := make(chan error, 1)
		go func() {
			_, err := d.prov.WriteAt(data, int64(from))
			errchan <- err
		}()

		// Wait until either the WriteAt completed, or our context is cancelled...
		select {
		case <-ctx.Done():
			return d.writeResponse(1, handle, []byte{})
		case err := <-errchan:
			if err != nil {
				return d.writeResponse(1, handle, []byte{})
			}
		}

		// write was successful - write response and record event including response time
		err := d.writeResponse(0, handle, []byte{})
		d.recordEvent(startTime, int64(from), int64(len(data)), true)
		return err
	}

	go func() {
		err := performWrite(cmdHandle, cmdFrom, cmdData)
		if err != nil {
			select {
			case d.fatal <- err:
			default:
				logger.L().Error(ctx, "nbd error cmd write", zap.Error(err))
			}
		}
		d.pendingResponses.Done()
	}()

	return nil
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
