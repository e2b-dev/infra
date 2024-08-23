package uffd

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"sync"
	"syscall"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/cache"
)

var memfileCache = cache.NewMmapfileCache()

type UffdSetup struct {
	Mappings []GuestRegionUffdMapping
	Fd       uintptr
}

func New(
	memfilePath,
	socketPath,
	envID,
	buildID string,
) (*Uffd, error) {
	pRead, pWrite, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create exit fd: %w", err)
	}

	return &Uffd{
		exitChan:    make(chan error),
		exitReader:  pRead,
		exitWriter:  pWrite,
		envID:       envID,
		buildID:     buildID,
		memfilePath: memfilePath,
		socketPath:  socketPath,
		Stop: sync.OnceValue(func() error {
			_, writeErr := pWrite.Write([]byte{0})
			if writeErr != nil {
				return fmt.Errorf("failed write to exit writer: %w", writeErr)
			}

			return nil
		}),
	}, nil
}

type Uffd struct {
	exitChan chan error

	exitReader *os.File
	exitWriter *os.File

	Stop func() error

	lis net.Listener

	socketPath  string
	memfilePath string

	envID   string
	buildID string
}

func (u *Uffd) Start() error {
	mf, err := memfileCache.GetMmapfile(u.memfilePath, fmt.Sprintf("%s-%s", u.envID, u.buildID))
	if err != nil {
		return fmt.Errorf("failed to get mmapfile: %w", err)
	}

	lis, err := net.ListenUnix("unix", &net.UnixAddr{Name: u.socketPath, Net: "unix"})
	if err != nil {
		return fmt.Errorf("failed listening on socket: %w", err)
	}

	u.lis = lis

	err = os.Chmod(u.socketPath, 0o777)
	if err != nil {
		return fmt.Errorf("failed setting socket permissions: %w", err)
	}

	go func() {
		u.exitChan <- u.handle(mf)
		close(u.exitChan)
	}()

	return nil
}

func (u *Uffd) receiveSetup() (*UffdSetup, error) {
	conn, err := u.lis.Accept()
	if err != nil {
		return nil, fmt.Errorf("failed accepting firecracker connection: %w", err)
	}

	unixConn := conn.(*net.UnixConn)

	mappingsBuf := make([]byte, 1024)
	uffdBuf := make([]byte, syscall.CmsgSpace(4))

	numBytesMappings, numBytesFd, _, _, err := unixConn.ReadMsgUnix(mappingsBuf, uffdBuf)
	if err != nil {
		return nil, fmt.Errorf("failed to read unix msg from connection: %w", err)
	}

	mappingsBuf = mappingsBuf[:numBytesMappings]

	var mappings []GuestRegionUffdMapping

	err = json.Unmarshal(mappingsBuf, &mappings)
	if err != nil {
		return nil, fmt.Errorf("failed parsing memory mapping data: %w", err)
	}

	controlMsgs, err := syscall.ParseSocketControlMessage(uffdBuf[:numBytesFd])
	if err != nil {
		return nil, fmt.Errorf("failed parsing control messages: %w", err)
	}

	if len(controlMsgs) != 1 {
		return nil, fmt.Errorf("expected 1 control message containing UFFD: found %d", len(controlMsgs))
	}

	fds, err := syscall.ParseUnixRights(&controlMsgs[0])
	if err != nil {
		return nil, fmt.Errorf("failed parsing unix write: %w", err)
	}

	if len(fds) != 1 {
		return nil, fmt.Errorf("expected 1 fd: found %d", len(fds))
	}

	return &UffdSetup{
		Mappings: mappings,
		Fd:       uintptr(fds[0]),
	}, nil
}

func (u *Uffd) handle(memory *cache.Mmapfile) (err error) {
	setup, err := u.receiveSetup()
	if err != nil {
		return fmt.Errorf("failed to receive setup message from firecracker: %w", err)
	}

	uffd := setup.Fd
	defer syscall.Close(int(uffd))

	err = Serve(int(uffd), setup.Mappings, memory, u.exitReader.Fd())
	if err != nil {
		return fmt.Errorf("failed handling uffd: %w", err)
	}

	return nil
}

func (u *Uffd) Wait() error {
	handleErr := <-u.exitChan

	closeErr := u.lis.Close()
	writerErr := u.exitWriter.Close()

	return errors.Join(handleErr, closeErr, writerErr)
}
