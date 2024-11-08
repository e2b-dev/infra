package nbd

import (
	"fmt"
	"net"
	"os"
	"sync"
	"syscall"
	"time"

	"github.com/pojntfx/go-nbd/pkg/backend"
	"github.com/pojntfx/go-nbd/pkg/client"
	"github.com/pojntfx/go-nbd/pkg/server"
	"github.com/pojntfx/r3map/pkg/utils"
)

type DirectPathMount struct {
	e *server.Export
	f *os.File

	serverOptions *server.Options
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
	f *os.File,

	serverOptions *server.Options,
	clientOptions *client.Options,
) *DirectPathMount {
	return &DirectPathMount{
		e: &server.Export{
			Name:    "default",
			Backend: b,
		},
		f: f,

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
openLoop:
	for {
		fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
		if err != nil {
			return err
		}

		go func() {
			d.sf = os.NewFile(uintptr(fds[0]), "server")

			c, err := net.FileConn(d.sf)
			if err != nil {
				d.errs <- err

				return
			}

			d.sc = c.(*net.UnixConn)

			if err := server.Handle(
				d.sc,
				[]*server.Export{d.e},
				d.serverOptions,
			); err != nil {
				fmt.Printf("Handle server error: %v\n", err)
				if !utils.IsClosedErr(err) {
					d.errs <- err
				}

				return
			}
		}()

		ready := make(chan struct{})

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

			d.clientOptions.OnConnected = func() {
				ready <- struct{}{}
			}

			if err := client.Connect(d.cc, d.f, d.clientOptions); err != nil {
				if !utils.IsClosedErr(err) {
					d.errs <- err
				}

				return
			}
		}()

		select {
		case err := <-d.errs:
			if err != nil {
				fmt.Printf("Error: %v\n", err)
				d.Close()
				break
			}
		case <-ready:
			fmt.Printf("Ready\n")
			break openLoop
		}

		time.Sleep(50 * time.Millisecond)
	}

	return nil
}

func (d *DirectPathMount) Close() {
	d.closeLock.Lock()
	defer d.closeLock.Unlock()

	_ = client.Disconnect(d.f)

	if d.cc != nil {
		_ = d.cc.Close()
	}

	if d.cf != nil {
		_ = d.cf.Close()
	}

	if d.sc != nil {
		_ = d.sc.Close()
	}

	if d.sf != nil {
		_ = d.sf.Close()
	}

	if d.errs != nil {
		close(d.errs)

		d.errs = nil
	}

	return
}

func (d *DirectPathMount) Sync() error {
	return nil
}

func (d *DirectPathMount) SwapBackend(b backend.Backend) {
	d.e.Backend = b
}
