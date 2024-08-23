package uffd

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"syscall"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/cache"
)

type UffdSetup struct {
	Mappings []GuestRegionUffdMapping
	Fd       uintptr
}

type Handler struct {
	lis      net.Listener
	exitChan chan error

	exitReader *os.File
	exitWriter *os.File

	pageSize int
}

func (h *Handler) Start(socketPath string, memory *cache.Mmapfile) error {
	lis, err := net.ListenUnix("unix", &net.UnixAddr{Name: socketPath, Net: "unix"})
	if err != nil {
		return fmt.Errorf("failed listening on socket: %w", err)
	}

	h.lis = lis

	err = os.Chmod(socketPath, 0o777)
	if err != nil {
		return fmt.Errorf("failed setting socket permissions: %w", err)
	}

	h.exitChan = make(chan error)

	pRead, pWrite, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("failed to create exit fd: %w", err)
	}

	h.exitReader = pRead
	h.exitWriter = pWrite

	go func() {
		h.exitChan <- h.handle(memory)
		close(h.exitChan)
	}()

	return nil
}

func (h *Handler) receiveSetupMsg() (*UffdSetup, error) {
	conn, err := h.lis.Accept()
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

func (h *Handler) handle(memory *cache.Mmapfile) (err error) {
	setup, err := h.receiveSetupMsg()
	if err != nil {
		return fmt.Errorf("failed to receive setup message from firecracker: %w", err)
	}

	uffd := setup.Fd
	defer syscall.Close(int(uffd))

	err = Serve(int(uffd), setup.Mappings, memory, h.exitReader.Fd())
	if err != nil {
		return fmt.Errorf("failed handling uffd: %w", err)
	}

	return nil
}

func (h *Handler) Wait() error {
	handleErr := <-h.exitChan

	closeErr := h.lis.Close()
	writerErr := h.exitWriter.Close()

	return errors.Join(handleErr, closeErr, writerErr)
}

func (h *Handler) Stop() error {
	_, err := h.exitWriter.Write([]byte{0})
	if err != nil {
		return fmt.Errorf("failed write to exit writer: %w", err)
	}

	return nil
}
