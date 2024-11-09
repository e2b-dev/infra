package nbd

import (
	"fmt"
	"net"
	"os"
	"sync"
	"syscall"

	"github.com/pojntfx/go-nbd/pkg/backend"
	"github.com/pojntfx/go-nbd/pkg/client"
	"github.com/pojntfx/r3map/pkg/utils"
)

type DirectPathMount struct {
	devPath string

	e *Export
	f *os.File

	serverOptions *Options
	clientOptions *client.Options

	sf *os.File
	sc *net.UnixConn

	cf *os.File
	cc *net.UnixConn

	closeLock sync.Mutex

	errs chan error
}

func NewDirectPathMount(
	b backend.Backend,
	devPath string,

	serverOptions *Options,
	clientOptions *client.Options,
) *DirectPathMount {
	return &DirectPathMount{
		e: &Export{
			Name:    "default",
			Backend: b,
		},
		devPath: devPath,

		serverOptions: serverOptions,
		clientOptions: clientOptions,

		errs: make(chan error),
	}
}

func (d *DirectPathMount) Wait() error {
	for err := range d.errs {
		if err != nil {
			return err
		}
	}

	return nil
}

func (d *DirectPathMount) Open() error {
	errs := make(chan error)
	retryCounter := 0

loop:
	for {
		f, err := os.Open(d.devPath)
		if err != nil {
			return err
		}

		d.f = f

		fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
		if err != nil {
			return err
		}

		ready := make(chan struct{})

		go func() {
			d.sf = os.NewFile(uintptr(fds[0]), "server")

			c, err := net.FileConn(d.sf)
			if err != nil {
				d.errs <- err

				return
			}

			d.sc = c.(*net.UnixConn)

			if err := Handle(
				d.sc,
				[]*Export{d.e},
				d.serverOptions,
				ready,
			); err != nil {
				fmt.Printf("Handle server error: %v\n", err)
				if !utils.IsClosedErr(err) {
					errs <- err
				}

				return
			}
		}()

		go func() {
			d.cf = os.NewFile(uintptr(fds[1]), "client")

			c, err := net.FileConn(d.cf)
			if err != nil {
				d.errs <- err

				return
			}

			d.cc = c.(*net.UnixConn)

			if d.clientOptions == nil {
				d.clientOptions = &client.Options{}
			}

			if err := client.Connect(d.cc, d.f, d.clientOptions); err != nil {
				if !utils.IsClosedErr(err) {
					errs <- err
				}

				return
			}
		}()

		select {
		case err := <-errs:
			if err != nil {
				if retryCounter > 5 {
					d.errs <- err
					return fmt.Errorf("failed to open device %s after %d retries", d.devPath, retryCounter)
				}

				fmt.Printf("Error while opening network block device %s: %v\n", d.devPath, err)
				break
			}
		case <-ready:
			break loop
		}

		retryCounter++
	}

	return nil
}

func (d *DirectPathMount) Close() {
	d.closeLock.Lock()
	defer d.closeLock.Unlock()

	client.Disconnect(d.f)

	if d.cc != nil {
		d.cc.Close()
	}

	if d.cf != nil {
		d.cf.Close()
	}

	if d.sc != nil {
		d.sc.Close()
	}

	if d.sf != nil {
		d.sf.Close()
	}

	if d.f != nil {
		d.f.Close()
	}

	if d.errs != nil {
		close(d.errs)

		d.errs = nil
	}

	return
}

// TODO: remove, only for mock
func (d *DirectPathMount) ReadAt(data []byte, offset int64) (int, error) {
	return d.f.ReadAt(data, offset)
}

func (d *DirectPathMount) Sync() error {
	return nil
}

func (d *DirectPathMount) SwapBackend(b backend.Backend) {
	d.e.Backend = b
}
