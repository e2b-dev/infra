package buse

import (
	"os"
)

// Rewrote type definitions for #defines and structs to workaround cgo
// as defined in <linux/nbd.h>

const (
	NBD_SET_SOCK        = (0xab<<8 | 0)
	NBD_SET_BLKSIZE     = (0xab<<8 | 1)
	NBD_SET_SIZE        = (0xab<<8 | 2)
	NBD_DO_IT           = (0xab<<8 | 3)
	NBD_CLEAR_SOCK      = (0xab<<8 | 4)
	NBD_CLEAR_QUE       = (0xab<<8 | 5)
	NBD_PRINT_DEBUG     = (0xab<<8 | 6)
	NBD_SET_SIZE_BLOCKS = (0xab<<8 | 7)
	NBD_DISCONNECT      = (0xab<<8 | 8)
	NBD_SET_TIMEOUT     = (0xab<<8 | 9)
	NBD_SET_FLAGS       = (0xab<<8 | 10)
)

const (
	NBD_CMD_READ  = 0
	NBD_CMD_WRITE = 1
	NBD_CMD_DISC  = 2
	NBD_CMD_FLUSH = 3
	NBD_CMD_TRIM  = 4
)

const (
	NBD_FLAG_HAS_FLAGS  = (1 << 0)
	NBD_FLAG_READ_ONLY  = (1 << 1)
	NBD_FLAG_SEND_FLUSH = (1 << 2)
	NBD_FLAG_SEND_TRIM  = (1 << 5)
)

const (
	NBD_REQUEST_MAGIC = 0x25609513
	NBD_REPLY_MAGIC   = 0x67446698
)

type nbdRequest struct {
	Magic  uint32
	Type   uint32
	Handle uint64
	From   uint64
	Length uint32
}

type nbdReply struct {
	Magic  uint32
	Error  uint32
	Handle uint64
}

type BuseInterface interface {
	ReadAt(p []byte, off uint) error
	WriteAt(p []byte, off uint) error
	Disconnect()
	Flush() error
	Trim(off uint, length uint) error
}

type BuseDevice struct {
	size       uint
	device     string
	driver     BuseInterface
	deviceFp   *os.File
	socketPair [2]int
	op         [5]func(driver BuseInterface, fp *os.File, chunk []byte, request *nbdRequest, reply *nbdReply) error
	disconnect chan int
}
