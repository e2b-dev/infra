package buse

import (
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"os"
	"syscall"
	"unsafe"
)

func ioctl(fd, op, arg uintptr) {
	_, _, ep := syscall.Syscall(syscall.SYS_IOCTL, fd, op, arg)
	if ep != 0 {
		log.Fatalf("ioctl(%d, %d, %d) failed: %s", fd, op, arg, syscall.Errno(ep))
	}
}

func opDeviceRead(driver BuseInterface, fp *os.File, chunk []byte, request *nbdRequest, reply *nbdReply) error {
	if err := driver.ReadAt(chunk, uint(request.From)); err != nil {
		log.Println("buseDriver.ReadAt returned an error:", err)
		// Reply with an EPERM
		reply.Error = 1
	}
	buf := writeNbdReply(reply)
	if _, err := fp.Write(buf); err != nil {
		log.Println("Write error, when sending reply header:", err)
	}
	if _, err := fp.Write(chunk); err != nil {
		log.Println("Write error, when sending data chunk:", err)
	}
	return nil
}

func opDeviceWrite(driver BuseInterface, fp *os.File, chunk []byte, request *nbdRequest, reply *nbdReply) error {
	if _, err := io.ReadFull(fp, chunk); err != nil {
		return fmt.Errorf("Fatal error, cannot read request packet: %s", err)
	}
	if err := driver.WriteAt(chunk, uint(request.From)); err != nil {
		log.Println("buseDriver.WriteAt returned an error:", err)
		reply.Error = 1
	}
	buf := writeNbdReply(reply)
	if _, err := fp.Write(buf); err != nil {
		log.Println("Write error, when sending reply header:", err)
	}
	return nil
}

func opDeviceDisconnect(driver BuseInterface, fp *os.File, chunk []byte, request *nbdRequest, reply *nbdReply) error {
	log.Println("Calling buseDriver.Disconnect()")
	driver.Disconnect()
	return fmt.Errorf("Received a disconnect")
}

func opDeviceFlush(driver BuseInterface, fp *os.File, chunk []byte, request *nbdRequest, reply *nbdReply) error {
	if err := driver.Flush(); err != nil {
		log.Println("buseDriver.Flush returned an error:", err)
		reply.Error = 1
	}
	buf := writeNbdReply(reply)
	if _, err := fp.Write(buf); err != nil {
		log.Println("Write error, when sending reply header:", err)
	}
	return nil
}

func opDeviceTrim(driver BuseInterface, fp *os.File, chunk []byte, request *nbdRequest, reply *nbdReply) error {
	if err := driver.Trim(uint(request.From), uint(request.Length)); err != nil {
		log.Println("buseDriver.Flush returned an error:", err)
		reply.Error = 1
	}
	buf := writeNbdReply(reply)
	if _, err := fp.Write(buf); err != nil {
		log.Println("Write error, when sending reply header:", err)
	}
	return nil
}

func (bd *BuseDevice) startNBDClient() {
	ioctl(bd.deviceFp.Fd(), NBD_SET_SOCK, uintptr(bd.socketPair[1]))
	// The call below may fail on some systems (if flags unset), could be ignored
	ioctl(bd.deviceFp.Fd(), NBD_SET_FLAGS, NBD_FLAG_SEND_TRIM)
	// The following call will block until the client disconnects
	log.Println("Starting NBD client...")
	go ioctl(bd.deviceFp.Fd(), NBD_DO_IT, 0)
	// Block on the disconnect channel
	<-bd.disconnect
}

// Disconnect disconnects the BuseDevice
func (bd *BuseDevice) Disconnect() {
	bd.disconnect <- 1
	// Ok to fail, ignore errors
	syscall.Syscall(syscall.SYS_IOCTL, bd.deviceFp.Fd(), NBD_CLEAR_QUE, 0)
	syscall.Syscall(syscall.SYS_IOCTL, bd.deviceFp.Fd(), NBD_DISCONNECT, 0)
	syscall.Syscall(syscall.SYS_IOCTL, bd.deviceFp.Fd(), NBD_CLEAR_SOCK, 0)
	// Cleanup fd
	syscall.Close(bd.socketPair[0])
	syscall.Close(bd.socketPair[1])
	bd.deviceFp.Close()
	log.Println("NBD client disconnected")
}

func readNbdRequest(buf []byte, request *nbdRequest) {
	request.Magic = binary.BigEndian.Uint32(buf)
	request.Type = binary.BigEndian.Uint32(buf[4:8])
	request.Handle = binary.BigEndian.Uint64(buf[8:16])
	request.From = binary.BigEndian.Uint64(buf[16:24])
	request.Length = binary.BigEndian.Uint32(buf[24:28])
}

func writeNbdReply(reply *nbdReply) []byte {
	buf := make([]byte, unsafe.Sizeof(*reply))
	binary.BigEndian.PutUint32(buf[0:4], NBD_REPLY_MAGIC)
	binary.BigEndian.PutUint32(buf[4:8], reply.Error)
	binary.BigEndian.PutUint64(buf[8:16], reply.Handle)
	// NOTE: a struct in go has 4 extra bytes, so we skip the last
	return buf[0:16]
}

// Connect connects a BuseDevice to an actual device file
// and starts handling requests. It does not return until it's done serving requests.
func (bd *BuseDevice) Connect() error {
	go bd.startNBDClient()
	defer bd.Disconnect()
	//opens the device file at least once, to make sure the partition table is updated
	tmp, err := os.Open(bd.device)
	if err != nil {
		return fmt.Errorf("Cannot reach the device %s: %s", bd.device, err)
	}
	tmp.Close()
	// Start handling requests
	request := nbdRequest{}
	reply := nbdReply{Magic: NBD_REPLY_MAGIC}
	fp := os.NewFile(uintptr(bd.socketPair[0]), "unix")
	// NOTE: a struct in go has 4 extra bytes...
	buf := make([]byte, unsafe.Sizeof(request))
	for true {
		if _, err := fp.Read(buf[0:28]); err != nil {
			return fmt.Errorf("NBD client stopped: %s", err)
		}
		readNbdRequest(buf, &request)
		fmt.Printf("DEBUG %+v\n", request)
		if request.Magic != NBD_REQUEST_MAGIC {
			return fmt.Errorf("Fatal error: received packet with wrong Magic number")
		}
		reply.Handle = request.Handle
		chunk := make([]byte, request.Length)
		reply.Error = 0
		// Dispatches READ, WRITE, DISC, FLUSH, TRIM to the corresponding implementation
		if request.Type < NBD_CMD_READ || request.Type > NBD_CMD_TRIM {
			log.Println("Received unknown request:", request.Type)
			continue
		}
		if err := bd.op[request.Type](bd.driver, fp, chunk, &request, &reply); err != nil {
			return err
		}
	}
	return nil
}

func CreateDevice(device string, size uint, buseDriver BuseInterface) (*BuseDevice, error) {
	buseDevice := &BuseDevice{size: size, device: device, driver: buseDriver}
	sockPair, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		return nil, fmt.Errorf("Call to socketpair failed: %s", err)
	}
	fp, err := os.OpenFile(device, os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("Cannot open \"%s\". Make sure the `nbd' kernel module is loaded: %s", device, err)
	}
	buseDevice.deviceFp = fp
	ioctl(buseDevice.deviceFp.Fd(), NBD_SET_SIZE, uintptr(size))
	ioctl(buseDevice.deviceFp.Fd(), NBD_CLEAR_QUE, 0)
	ioctl(buseDevice.deviceFp.Fd(), NBD_CLEAR_SOCK, 0)
	buseDevice.socketPair = sockPair
	buseDevice.op[NBD_CMD_READ] = opDeviceRead
	buseDevice.op[NBD_CMD_WRITE] = opDeviceWrite
	buseDevice.op[NBD_CMD_DISC] = opDeviceDisconnect
	buseDevice.op[NBD_CMD_FLUSH] = opDeviceFlush
	buseDevice.op[NBD_CMD_TRIM] = opDeviceTrim
	buseDevice.disconnect = make(chan int, 5)
	return buseDevice, nil
}