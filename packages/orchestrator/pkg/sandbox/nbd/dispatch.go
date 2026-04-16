package nbd

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

var (
	nbdReadDuration = utils.Must(meter.Int64Histogram("orchestrator.nbd.dispatch.read.duration",
		metric.WithDescription("Duration of NBD dispatch handler ReadAt calls to the backend."),
		metric.WithUnit("ms"),
	))
	nbdReadSuccess   = metric.WithAttributeSet(attribute.NewSet(attribute.String("result", "success")))
	nbdReadFailure   = metric.WithAttributeSet(attribute.NewSet(attribute.String("result", "failure")))
	nbdReadCancelled = metric.WithAttributeSet(attribute.NewSet(attribute.String("result", "cancelled")))
)

type Provider interface {
	ReadSlices(ctx context.Context, off, length int64, dest [][]byte) ([][]byte, error)
	WriteSlice(off, length int64) (dest []byte, done func(commit bool), err error)
	Size(ctx context.Context) (int64, error)
	BlockSize() int64
}

const (
	// With zero-copy writes going directly into the mmap, the dispatch buffer
	// only needs to hold NBD request headers (28 bytes each).
	dispatchBufferSize = 4 * 1024

	// https://sourceforge.net/p/nbd/mailman/message/35081223/
	// 32 MB is the maximum request size universally supported by the kernel NBD driver.
	// Reject anything larger to protect against OOM from a corrupted header.
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
	fp       io.ReadWriter
	respHdr  [16]byte
	prov     Provider
	readBufs [][]byte // persistent per-block slice storage, reused across reads
	readBuf  []byte   // reused across reads for contiguous response data
	writeBuf []byte   // reused across writes to avoid per-request allocation
	buffer   []byte
}

func NewDispatch(fp io.ReadWriter, prov Provider) *Dispatch {
	return &Dispatch{
		fp:     fp,
		prov:   prov,
		buffer: make([]byte, dispatchBufferSize),
	}
}

func (d *Dispatch) Drain() {}

func (d *Dispatch) writeResponse(respError uint32, respHandle uint64, chunk []byte) error {
	binary.BigEndian.PutUint32(d.respHdr[0:4], NBDResponseMagic)
	binary.BigEndian.PutUint32(d.respHdr[4:8], respError)
	binary.BigEndian.PutUint64(d.respHdr[8:16], respHandle)

	_, err := d.fp.Write(d.respHdr[:])
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

func (d *Dispatch) writeReadResponse(respHandle uint64, slices [][]byte) error {
	binary.BigEndian.PutUint32(d.respHdr[0:4], NBDResponseMagic)
	binary.BigEndian.PutUint32(d.respHdr[4:8], 0)
	binary.BigEndian.PutUint64(d.respHdr[8:16], respHandle)

	_, err := d.fp.Write(d.respHdr[:])
	if err != nil {
		return err
	}

	for _, s := range slices {
		if _, err := d.fp.Write(s); err != nil {
			return err
		}
	}

	return nil
}

/**
 * This dispatches incoming NBD requests synchronously to the provider.
 *
 */
func (d *Dispatch) Handle(ctx context.Context) error {
	buffer := d.buffer
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
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}

			// Make sure we have a complete header
			if wp-rp < 28 {
				break // Try again when we have more data...
			}

			// We can read the header...

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

				writeLen := int(request.Length)

				if writeLen > dispatchMaxWriteBufferSize {
					return fmt.Errorf("nbd write request length %d exceeds maximum %d", writeLen, dispatchMaxWriteBufferSize)
				}

				// Ensure write buffer is large enough
				if cap(d.writeBuf) < writeLen {
					d.writeBuf = make([]byte, writeLen)
				}
				writeBuf := d.writeBuf[:writeLen]

				// Copy any data already in the dispatch buffer
				dataCopied := copy(writeBuf, buffer[rp:wp])
				rp += dataCopied

				// Read remaining data from socket
				for dataCopied < writeLen {
					n, err := d.fp.Read(writeBuf[dataCopied:])
					if err != nil {
						return fmt.Errorf("nbd write read error: %w", err)
					}

					dataCopied += n
				}

				// Acquire mmap lock briefly to copy data in
				dest, done, err := d.prov.WriteSlice(int64(request.From), int64(request.Length))
				if err != nil {
					return fmt.Errorf("nbd write slice error: %w", err)
				}

				copy(dest, writeBuf)
				done(true)

				err = d.writeResponse(0, request.Handle, nil)
				if err != nil {
					return err
				}
			case NBDCmdTrim:
				rp += 28
				err := d.writeResponse(0, request.Handle, nil)
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
	start := time.Now()

	slices, err := d.prov.ReadSlices(ctx, int64(cmdFrom), int64(cmdLength), d.readBufs)
	d.readBufs = slices

	attrs := nbdReadSuccess
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			attrs = nbdReadCancelled
		} else {
			attrs = nbdReadFailure
		}
	}

	nbdReadDuration.Record(ctx, time.Since(start).Milliseconds(), attrs)

	if err != nil {
		return d.writeResponse(1, cmdHandle, nil)
	}

	readLen := int(cmdLength)
	if cap(d.readBuf) < readLen {
		d.readBuf = make([]byte, readLen)
	}
	buf := d.readBuf[:readLen]
	off := 0
	for _, s := range slices {
		off += copy(buf[off:], s)
	}

	return d.writeResponse(0, cmdHandle, buf)
}
