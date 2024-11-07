package nbd

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/pilebones/go-udev/netlink"
	"github.com/pojntfx/go-nbd/pkg/ioctl"
	"github.com/pojntfx/go-nbd/pkg/protocol"
	"github.com/pojntfx/go-nbd/pkg/server"
)

const (
	MinimumBlockSize = 512  // This is the minimum value that works in practice, else the client stops with "invalid argument"
	MaximumBlockSize = 4096 // This is the maximum value that works in practice, else the client stops with "invalid argument"
)

var (
	ErrUnsupportedNetwork         = errors.New("unsupported network")
	ErrUnknownReply               = errors.New("unknown reply")
	ErrUnknownInfo                = errors.New("unknown info")
	ErrUnknownErr                 = errors.New("unknown error")
	ErrUnsupportedServerBlockSize = errors.New("server proposed unsupported block size")
	ErrMinimumBlockSize           = errors.New("block size below mimimum requested")
	ErrMaximumBlockSize           = errors.New("block size above maximum requested")
	ErrBlockSizeNotPowerOfTwo     = errors.New("block size is not a power of 2")
)

type ClientOptions struct {
	ExportName             string
	BlockSize              uint32
	OnConnected            func()
	ReadyCheckUdev         bool
	ReadyCheckPollInterval time.Duration
	Timeout                int
}

func negotiateNewstyle(conn net.Conn) error {
	var newstyleHeader protocol.NegotiationNewstyleHeader
	if err := binary.Read(conn, binary.BigEndian, &newstyleHeader); err != nil {
		return err
	}

	if newstyleHeader.OldstyleMagic != protocol.NEGOTIATION_MAGIC_OLDSTYLE {
		return server.ErrInvalidMagic
	}

	if newstyleHeader.OptionMagic != protocol.NEGOTIATION_MAGIC_OPTION {
		return server.ErrInvalidMagic
	}

	if _, err := conn.Write(make([]byte, 4)); err != nil { // Send client flags (uint32)
		return err
	}

	return nil
}

func Connect(conn net.Conn, device *os.File, options *ClientOptions) error {
	if options == nil {
		options = &ClientOptions{}
	}

	if options.ExportName == "" {
		options.ExportName = "default"
	}

	if !options.ReadyCheckUdev && options.ReadyCheckPollInterval <= 0 {
		options.ReadyCheckPollInterval = time.Millisecond
	}

	var cfd uintptr
	switch c := conn.(type) {
	case *net.TCPConn:
		file, err := c.File()
		if err != nil {
			return err
		}

		cfd = uintptr(file.Fd())
	case *net.UnixConn:
		file, err := c.File()
		if err != nil {
			fmt.Printf("Client Unix Conn Error: %v\n", err)
			return err
		}

		cfd = uintptr(file.Fd())
	default:
		return ErrUnsupportedNetwork
	}

	fatal := make(chan error)
	if options.OnConnected != nil {
		if options.ReadyCheckUdev {
			udevConn := new(netlink.UEventConn)
			if err := udevConn.Connect(netlink.UdevEvent); err != nil {
				fmt.Printf("Udev Connect Error: %v\n", err)
				return err
			}
			defer udevConn.Close()

			var (
				udevReadyCh = make(chan netlink.UEvent)
				udevErrCh   = make(chan error)
				udevQuit    = udevConn.Monitor(udevReadyCh, udevErrCh, &netlink.RuleDefinitions{
					Rules: []netlink.RuleDefinition{
						{
							Env: map[string]string{
								"DEVNAME": device.Name(),
							},
						},
					},
				})
			)
			defer close(udevQuit)

			go func() {
				select {
				case <-udevReadyCh:
					close(udevQuit)

					options.OnConnected()

					return
				case err := <-udevErrCh:
					fatal <- err

					return
				}
			}()
		} else {
			go func() {
				sizeFile, err := os.Open(filepath.Join("/sys", "block", filepath.Base(device.Name()), "size"))
				if err != nil {
					fatal <- err

					return
				}
				defer sizeFile.Close()

				for {
					if _, err := sizeFile.Seek(0, io.SeekStart); err != nil {
						fatal <- err

						return
					}

					rsize, err := io.ReadAll(sizeFile)
					if err != nil {
						fatal <- err

						return
					}

					size, err := strconv.ParseInt(strings.TrimSpace(string(rsize)), 10, 64)
					if err != nil {
						fatal <- err

						return
					}

					if size > 0 {
						options.OnConnected()

						return
					}

					time.Sleep(options.ReadyCheckPollInterval)
				}
			}()
		}
	}

	if _, _, err := syscall.Syscall(
		syscall.SYS_IOCTL,
		device.Fd(),
		ioctl.NEGOTIATION_IOCTL_SET_SOCK,
		uintptr(cfd),
	); err != 0 {
		return err
	}

	if err := negotiateNewstyle(conn); err != nil {
		return err
	}

	if err := binary.Write(conn, binary.BigEndian, protocol.NegotiationOptionHeader{
		OptionMagic: protocol.NEGOTIATION_MAGIC_OPTION,
		ID:          protocol.NEGOTIATION_ID_OPTION_GO,
		Length:      0,
	}); err != nil {
		return err
	}

	exportName := []byte(options.ExportName)

	if err := binary.Write(conn, binary.BigEndian, uint32(len(exportName))); err != nil {
		return err
	}

	if _, err := conn.Write([]byte(exportName)); err != nil {
		return err
	}

	if err := binary.Write(conn, binary.BigEndian, uint16(0)); err != nil { // Send information request count (uint16)
		return err
	}

	size := uint64(0)
	chosenBlockSize := uint32(1)

n:
	for {
		var replyHeader protocol.NegotiationReplyHeader
		if err := binary.Read(conn, binary.BigEndian, &replyHeader); err != nil {
			return err
		}

		if replyHeader.ReplyMagic != protocol.NEGOTIATION_MAGIC_REPLY {
			return server.ErrInvalidMagic
		}

		switch replyHeader.Type {
		case protocol.NEGOTIATION_TYPE_REPLY_INFO:
			infoRaw := make([]byte, replyHeader.Length)
			if _, err := io.ReadFull(conn, infoRaw); err != nil {
				return err
			}

			var infoType uint16
			if err := binary.Read(bytes.NewBuffer(infoRaw), binary.BigEndian, &infoType); err != nil {
				return err
			}

			switch infoType {
			case protocol.NEGOTIATION_TYPE_INFO_EXPORT:
				var info protocol.NegotiationReplyInfo
				if err := binary.Read(bytes.NewBuffer(infoRaw), binary.BigEndian, &info); err != nil {
					return err
				}

				size = info.Size
			case protocol.NEGOTIATION_TYPE_INFO_NAME:
				// Discard export name
			case protocol.NEGOTIATION_TYPE_INFO_DESCRIPTION:
				// Discard export description
			case protocol.NEGOTIATION_TYPE_INFO_BLOCKSIZE:
				var info protocol.NegotiationReplyBlockSize
				if err := binary.Read(bytes.NewBuffer(infoRaw), binary.BigEndian, &info); err != nil {
					return err
				}

				if options.BlockSize == 0 {
					chosenBlockSize = info.PreferredBlockSize
				} else if options.BlockSize >= info.MinimumBlockSize && options.BlockSize <= info.MaximumBlockSize {
					chosenBlockSize = options.BlockSize
				} else {
					return ErrUnsupportedServerBlockSize
				}

				if chosenBlockSize > MaximumBlockSize {
					return ErrMaximumBlockSize
				} else if chosenBlockSize < MinimumBlockSize {
					return ErrMinimumBlockSize
				}

				if !((chosenBlockSize > 0) && ((chosenBlockSize & (chosenBlockSize - 1)) == 0)) {
					return ErrBlockSizeNotPowerOfTwo
				}
			default:
				return ErrUnknownInfo
			}
		case protocol.NEGOTIATION_TYPE_REPLY_ACK:
			break n
		case protocol.NEGOTIATION_TYPE_REPLY_ERR_UNKNOWN:
			return ErrUnknownErr
		default:
			return ErrUnknownReply
		}
	}

	if _, _, err := syscall.Syscall(
		syscall.SYS_IOCTL,
		device.Fd(),
		ioctl.NEGOTIATION_IOCTL_SET_BLOCKSIZE,
		uintptr(chosenBlockSize),
	); err != 0 {
		return err
	}

	if _, _, err := syscall.Syscall(
		syscall.SYS_IOCTL,
		device.Fd(),
		ioctl.NEGOTIATION_IOCTL_SET_SIZE_BLOCKS,
		uintptr(size/uint64(chosenBlockSize)),
	); err != 0 {
		return err
	}

	if _, _, err := syscall.Syscall(
		syscall.SYS_IOCTL,
		device.Fd(),
		ioctl.NEGOTIATION_IOCTL_SET_TIMEOUT,
		uintptr(options.Timeout),
	); err != 0 {
		return err
	}

	go func() {
		defer func() {
			close(fatal)
		}()

		time.Sleep(50 * time.Millisecond)
		if _, _, err := syscall.Syscall(
			syscall.SYS_IOCTL,
			device.Fd(),
			ioctl.NEGOTIATION_IOCTL_DO_IT,
			0,
		); err != 0 {
			fatal <- err

			return
		}
	}()

	return <-fatal
}

func Disconnect(device *os.File) error {
	if _, _, err := syscall.Syscall(
		syscall.SYS_IOCTL,
		device.Fd(),
		ioctl.TRANSMISSION_IOCTL_CLEAR_QUE,
		0,
	); err != 0 {
		return err
	}

	if _, _, err := syscall.Syscall(
		syscall.SYS_IOCTL,
		device.Fd(),
		ioctl.TRANSMISSION_IOCTL_DISCONNECT,
		0,
	); err != 0 {
		return err
	}

	if _, _, err := syscall.Syscall(
		syscall.SYS_IOCTL,
		device.Fd(),
		ioctl.TRANSMISSION_IOCTL_CLEAR_SOCK,
		0,
	); err != 0 {
		return err
	}

	return nil
}

func List(conn net.Conn) ([]string, error) {
	if err := negotiateNewstyle(conn); err != nil {
		return []string{}, err
	}

	if err := binary.Write(conn, binary.BigEndian, protocol.NegotiationOptionHeader{
		OptionMagic: protocol.NEGOTIATION_MAGIC_OPTION,
		ID:          protocol.NEGOTIATION_ID_OPTION_LIST,
		Length:      0,
	}); err != nil {
		return []string{}, err
	}

	var replyHeader protocol.NegotiationReplyHeader
	if err := binary.Read(conn, binary.BigEndian, &replyHeader); err != nil {
		return []string{}, err
	}

	if replyHeader.ReplyMagic != protocol.NEGOTIATION_MAGIC_REPLY {
		return []string{}, server.ErrInvalidMagic
	}

	infoRaw := make([]byte, replyHeader.Length)
	if _, err := io.ReadFull(conn, infoRaw); err != nil {
		return []string{}, err
	}

	info := bytes.NewBuffer(infoRaw)

	exportNames := []string{}
	for {
		var exportNameLength uint32
		if err := binary.Read(info, binary.BigEndian, &exportNameLength); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}

			return []string{}, err
		}

		exportName := make([]byte, exportNameLength)
		if _, err := io.ReadFull(info, exportName); err != nil {
			return []string{}, err
		}

		exportNames = append(exportNames, string(exportName))
	}

	if err := binary.Write(conn, binary.BigEndian, protocol.NegotiationOptionHeader{
		OptionMagic: protocol.NEGOTIATION_MAGIC_OPTION,
		ID:          protocol.NEGOTIATION_ID_OPTION_ABORT,
		Length:      0,
	}); err != nil {
		return []string{}, err
	}

	return exportNames, nil
}
