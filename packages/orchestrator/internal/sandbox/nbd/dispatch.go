package nbd

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sync"

	"go.uber.org/zap"
)

var ErrShuttingDown = errors.New("shutting down. Cannot serve any new requests")

type Provider interface {
	io.ReaderAt
	io.WriterAt
	Size() (int64, error)
}

const dispatchBufferSize = 4 * 1024 * 1024

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
	ctx              context.Context // nolint:containedctx // todo: refactor so this can be removed
	fp               io.ReadWriteCloser
	responseHeader   []byte
	writeLock        sync.Mutex
	prov             Provider
	pendingResponses sync.WaitGroup
	shuttingDown     bool
	shuttingDownLock sync.Mutex
	fatal            chan error
}

func NewDispatch(ctx context.Context, fp io.ReadWriteCloser, prov Provider) *Dispatch {
	d := &Dispatch{
		responseHeader: make([]byte, 16),
		fp:             fp,
		prov:           prov,
		ctx:            ctx,
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
 * This dispatches incoming NBD requests sequentially to the provider.
 *
 */
func (d *Dispatch) Handle() error {
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
	process:
		for {
			// Check if there is a fatal error from an async read/write to return
			select {
			case err := <-d.fatal:
				return err
			case <-d.ctx.Done():
				return d.ctx.Err()
			default:
			}

			// Make sure we have a complete header
			if wp-rp >= 28 {
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
					err := d.cmdRead(request.Handle, request.From, request.Length)
					if err != nil {
						return err
					}
				case NBDCmdWrite:
					rp += 28
					if wp-rp < int(request.Length) {
						rp -= 28
						break process // We don't have enough data yet... Wait for next read
					}
					data := make([]byte, request.Length)
					copy(data, buffer[rp:rp+int(request.Length)])
					rp += int(request.Length)
					err := d.cmdWrite(request.Handle, request.From, data)
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
			} else {
				break // Try again when we have more data...
			}
		}
		// Now we need to move any partial to the start
		if rp != 0 && rp != wp {
			copy(buffer, buffer[rp:wp])
		}
		wp -= rp
	}
}

func (d *Dispatch) cmdRead(cmdHandle uint64, cmdFrom uint64, cmdLength uint32) error {
	d.shuttingDownLock.Lock()
	if !d.shuttingDown {
		d.pendingResponses.Add(1)
	} else {
		d.shuttingDownLock.Unlock()
		return ErrShuttingDown
	}
	d.shuttingDownLock.Unlock()

	performRead := func(handle uint64, from uint64, length uint32) error {
		// buffered to avoid goroutine leak
		errchan := make(chan error, 1)
		data := make([]byte, length)

		go func() {
			_, err := d.prov.ReadAt(data, int64(from))
			errchan <- err
		}()

		// Wait until either the ReadAt completed, or our context is cancelled...
		select {
		case <-d.ctx.Done():
			return d.writeResponse(1, handle, []byte{})
		case err := <-errchan:
			if err != nil {
				return d.writeResponse(1, handle, []byte{})
			}
		}

		// read was successful
		return d.writeResponse(0, handle, data)
	}

	go func() {
		err := performRead(cmdHandle, cmdFrom, cmdLength)
		if err != nil {
			select {
			case d.fatal <- err:
			default:
				zap.L().Error("nbd error cmd read", zap.Error(err))
			}
		}
		d.pendingResponses.Done()
	}()

	return nil
}

func (d *Dispatch) cmdWrite(cmdHandle uint64, cmdFrom uint64, cmdData []byte) error {
	d.shuttingDownLock.Lock()
	if !d.shuttingDown {
		d.pendingResponses.Add(1)
	} else {
		d.shuttingDownLock.Unlock()
		return ErrShuttingDown
	}
	d.shuttingDownLock.Unlock()

	performWrite := func(handle uint64, from uint64, data []byte) error {
		// buffered to avoid goroutine leak
		errchan := make(chan error, 1)
		go func() {
			_, err := d.prov.WriteAt(data, int64(from))
			errchan <- err
		}()

		// Wait until either the WriteAt completed, or our context is cancelled...
		select {
		case <-d.ctx.Done():
			return d.writeResponse(1, handle, []byte{})
		case err := <-errchan:
			if err != nil {
				return d.writeResponse(1, handle, []byte{})
			}
		}

		// write was successful
		return d.writeResponse(0, handle, []byte{})
	}

	go func() {
		err := performWrite(cmdHandle, cmdFrom, cmdData)
		if err != nil {
			select {
			case d.fatal <- err:
			default:
				zap.L().Error("nbd error cmd write", zap.Error(err))
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
