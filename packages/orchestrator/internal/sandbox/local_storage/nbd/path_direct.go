package nbd

import (
	"log"
	"net"
	"os"
	"sync"
	"syscall"

	"github.com/pojntfx/go-nbd/pkg/backend"
	"github.com/pojntfx/go-nbd/pkg/client"
	"github.com/pojntfx/r3map/pkg/utils"
)

type DirectPathMount struct {
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
	f *os.File,

	serverOptions *Options,
	clientOptions *client.Options,
) *DirectPathMount {
	return &DirectPathMount{
		e: &Export{
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
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		return err
	}

	log.Printf("Socketpair: %v\n", fds)
	go func() {
		d.sf = os.NewFile(uintptr(fds[0]), "server")

		c, err := net.FileConn(d.sf)
		if err != nil {
			d.errs <- err

			return
		}

		d.sc = c.(*net.UnixConn)

		log.Printf("Starting the NBD server\n")
		if err := Handle(
			d.sc,
			[]*Export{d.e},
			d.serverOptions,
		); err != nil {
			log.Printf("Error handling server: %v\n", err)
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
			log.Printf("Connected to server\n")
			ready <- struct{}{}
		}

		log.Printf("Starting the NBD client\n")
		if err := client.Connect(d.cc, d.f, d.clientOptions); err != nil {
			log.Printf("Error connecting client: %v\n", err)
			if !utils.IsClosedErr(err) {
				d.errs <- err
			}

			return
		}
	}()

	log.Printf("Waiting for the client to connect\n")
	<-ready

	return nil
}

func (d *DirectPathMount) Close() error {
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

	return nil
}

func (d *DirectPathMount) Sync() error {
	return nil
}

func (d *DirectPathMount) SwapBackend(b backend.Backend) {
	d.e.Backend = b
}
