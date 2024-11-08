package nbd

import (
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/pojntfx/go-nbd/pkg/backend"
	"github.com/pojntfx/go-nbd/pkg/client"
	"github.com/pojntfx/go-nbd/pkg/server"
	"github.com/pojntfx/r3map/pkg/utils"
)

type DirectPathMount struct {
	devPath string

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
	devPath string,

	serverOptions *server.Options,
	clientOptions *client.Options,
) *DirectPathMount {
	return &DirectPathMount{
		e: &server.Export{
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
	counter := 0
	wg := sync.WaitGroup{}

openLoop:
	for {
		f, err := os.Open(d.devPath)
		if err != nil {
			return err
		}
		d.f = f

		wg.Wait()
		counter++
		fmt.Printf("[%d] Opening\n", counter)

		fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
		if err != nil {
			return err
		}

		go func() {
			wg.Add(1)
			defer wg.Done()

			d.sf = os.NewFile(uintptr(fds[0]), "server")

			c, err := net.FileConn(d.sf)
			if err != nil {
				errs <- err

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
					errs <- err
				}

				return
			}
		}()

		ready := make(chan struct{})

		go func() {
			wg.Add(1)
			defer wg.Done()

			d.cf = os.NewFile(uintptr(fds[1]), "client")

			c, err := net.FileConn(d.cf)
			if err != nil {
				errs <- err

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
					errs <- err
				}

				return
			}
		}()

		select {
		case err := <-errs:
			if err != nil {
				fmt.Printf("Error: %v\n", err)
				d.Close()
				break
			}
		case <-ready:
			fmt.Printf("Ready\n")
			break openLoop
		}

		os.Remove(fmt.Sprintf("/sys/block/nbd%s/pid", strings.TrimPrefix(d.devPath, "/dev/nbd")))
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

	if d.f != nil {
		_ = d.f.Close()
	}

	if d.errs != nil {
		close(d.errs)

		d.errs = nil
	}

	return
}

func (d *DirectPathMount) ReadAt(offset int64, size int) ([]byte, error) {
	buf := make([]byte, size)

	n, err := d.f.ReadAt(buf, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to read from device: %w", err)
	}

	return buf[:n], nil
}

func (d *DirectPathMount) Sync() error {
	return nil
}

func (d *DirectPathMount) SwapBackend(b backend.Backend) {
	d.e.Backend = b
}
