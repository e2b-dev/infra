package main

import (
	"encoding/binary"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const (
	sockPath  = "/tmp/memfd-ownership.sock"
	totalSize = 512 << 20 // 512 MiB
	chunkSize = 2 << 20   // 2 MiB hugepage-aligned
	workers   = 4
)

// MemfdDevice provides chunk-aligned reads from a memfd while it is being
// exported to disk in the background. Reads work immediately — before, during,
// and after export.
//
// Each chunk transitions through: memfd → [pwrite to disk] → onDisk=true → [punch_hole].
// The flag is set between pwrite and punch, so data always exists in at least one place.
type MemfdDevice struct {
	memfd    int
	diskFile *os.File
	diskFd   int
	size     int64
	chunk    int64

	onDisk     []atomic.Bool
	exportDone chan struct{}
	exportErr  error
}

func NewMemfdDevice(memfd int, diskPath string, size, chunk int64, nworkers int) (*MemfdDevice, error) {
	f, err := os.Create(diskPath)
	if err != nil {
		return nil, fmt.Errorf("create disk file: %w", err)
	}
	if err := f.Truncate(size); err != nil {
		f.Close()
		return nil, fmt.Errorf("truncate: %w", err)
	}

	d := &MemfdDevice{
		memfd:      memfd,
		diskFile:   f,
		diskFd:     int(f.Fd()),
		size:       size,
		chunk:      chunk,
		onDisk:     make([]atomic.Bool, size/chunk),
		exportDone: make(chan struct{}),
	}
	go d.runExport(nworkers)
	return d, nil
}

// ReadAt reads one chunk at a chunk-aligned offset.
// Safe to call concurrently, before/during/after export.
func (d *MemfdDevice) ReadAt(buf []byte, off int64) (int, error) {
	if off%d.chunk != 0 {
		return 0, fmt.Errorf("offset %d not aligned to chunk size %d", off, d.chunk)
	}
	if int64(len(buf)) != d.chunk {
		return 0, fmt.Errorf("buffer size %d != chunk size %d", len(buf), d.chunk)
	}
	idx := int(off / d.chunk)
	if idx < 0 || idx >= len(d.onDisk) {
		return 0, fmt.Errorf("offset %d out of range [0, %d)", off, d.size)
	}

	if d.onDisk[idx].Load() {
		return unix.Pread(d.diskFd, buf, off)
	}

	n, err := unix.Pread(d.memfd, buf, off)
	if err != nil {
		return n, err
	}

	// Seqlock re-check: if the flag flipped during our pread, the punch may
	// have raced us. Re-read from disk where pwrite already completed.
	if d.onDisk[idx].Load() {
		return unix.Pread(d.diskFd, buf, off)
	}
	return n, nil
}

// IsOnDisk reports whether a chunk has been exported to disk.
func (d *MemfdDevice) IsOnDisk(chunkIdx int) bool {
	return d.onDisk[chunkIdx].Load()
}

func (d *MemfdDevice) Wait() error {
	<-d.exportDone
	return d.exportErr
}

func (d *MemfdDevice) Close() error {
	return unix.Close(d.memfd)
}

func (d *MemfdDevice) runExport(nworkers int) {
	defer close(d.exportDone)

	chunks := int(d.size / d.chunk)
	chunk := int(d.chunk)

	pool := &sync.Pool{New: func() any {
		b := make([]byte, chunk)
		return &b
	}}

	var next atomic.Int64
	var wg sync.WaitGroup
	errs := make(chan error, nworkers)

	for range nworkers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				i := int(next.Add(1) - 1)
				if i >= chunks {
					return
				}
				off := int64(i * chunk)

				bp := pool.Get().(*[]byte)
				buf := *bp

				if _, err := unix.Pread(d.memfd, buf, off); err != nil {
					pool.Put(bp)
					errs <- fmt.Errorf("pread chunk %d: %w", i, err)
					return
				}
				if _, err := unix.Pwrite(d.diskFd, buf, off); err != nil {
					pool.Put(bp)
					errs <- fmt.Errorf("pwrite chunk %d: %w", i, err)
					return
				}
				pool.Put(bp)

				d.onDisk[i].Store(true)

				if err := unix.Fallocate(d.memfd,
					unix.FALLOC_FL_PUNCH_HOLE|unix.FALLOC_FL_KEEP_SIZE,
					off, int64(chunk)); err != nil {
					errs <- fmt.Errorf("punch_hole chunk %d: %w", i, err)
					return
				}
			}
		}()
	}

	wg.Wait()
	close(errs)
	d.exportErr = <-errs
}

// --- fd passing ---

func memfdStats(fd int) (logical, physical int64, err error) {
	var st syscall.Stat_t
	if err := syscall.Fstat(fd, &st); err != nil {
		return 0, 0, err
	}
	return st.Size, st.Blocks * 512, nil
}

func sendFd(conn *net.UnixConn, fd int, size, chunk int) error {
	meta := make([]byte, 16)
	binary.LittleEndian.PutUint64(meta[0:], uint64(size))
	binary.LittleEndian.PutUint64(meta[8:], uint64(chunk))
	_, _, err := conn.WriteMsgUnix(meta, unix.UnixRights(fd), nil)
	return err
}

func recvFd(conn *net.UnixConn) (fd, size, chunk int, err error) {
	meta := make([]byte, 16)
	oob := make([]byte, unix.CmsgSpace(4))
	n, oobn, _, _, err := conn.ReadMsgUnix(meta, oob)
	if err != nil {
		return 0, 0, 0, err
	}
	if n < 16 {
		return 0, 0, 0, fmt.Errorf("short metadata: %d bytes", n)
	}
	scms, err := unix.ParseSocketControlMessage(oob[:oobn])
	if err != nil {
		return 0, 0, 0, err
	}
	fds, err := unix.ParseUnixRights(&scms[0])
	if err != nil {
		return 0, 0, 0, err
	}
	return fds[0],
		int(binary.LittleEndian.Uint64(meta[0:])),
		int(binary.LittleEndian.Uint64(meta[8:])),
		nil
}

// --- demo ---

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "usage: %s <produce|consume> [--hugetlb]\n", os.Args[0])
		os.Exit(1)
	}

	hugetlb := false
	for _, a := range os.Args[2:] {
		if a == "--hugetlb" {
			hugetlb = true
		}
	}

	var err error
	switch os.Args[1] {
	case "produce":
		err = runProducer(hugetlb)
	case "consume":
		err = runConsumer()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
		os.Exit(1)
	}
	if err != nil {
		log.Fatal(err)
	}
}

func runProducer(hugetlb bool) error {
	flags := 0
	if hugetlb {
		flags = unix.MFD_HUGETLB | unix.MFD_HUGE_2MB
	}

	fd, err := unix.MemfdCreate("fc-guest-memory", flags)
	if err != nil && hugetlb {
		fd, err = unix.MemfdCreate("fc-guest-memory", 0)
	}
	if err != nil {
		return fmt.Errorf("memfd_create: %w", err)
	}
	if err := unix.Ftruncate(fd, totalSize); err != nil {
		return fmt.Errorf("ftruncate: %w", err)
	}

	data, err := unix.Mmap(fd, 0, totalSize, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
	if err != nil {
		return fmt.Errorf("mmap: %w", err)
	}
	for i := range totalSize / chunkSize {
		off := i * chunkSize
		for j := off; j < off+chunkSize; j++ {
			data[j] = 0xAA
		}
		binary.LittleEndian.PutUint64(data[off:], uint64(i))
	}

	printStats("producer", fd)

	os.Remove(sockPath)
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	defer os.Remove(sockPath)

	log.Printf("[producer] waiting on %s", sockPath)
	conn, err := ln.Accept()
	if err != nil {
		return fmt.Errorf("accept: %w", err)
	}
	ln.Close()

	if err := sendFd(conn.(*net.UnixConn), fd, totalSize, chunkSize); err != nil {
		return fmt.Errorf("send fd: %w", err)
	}

	ack := make([]byte, 1)
	conn.Read(ack)
	unix.Munmap(data)
	unix.Close(fd)
	log.Printf("[producer] exiting — memory survives in consumer")
	return nil
}

func runConsumer() error {
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}

	memfd, size, chunk, err := recvFd(conn.(*net.UnixConn))
	if err != nil {
		return fmt.Errorf("recv fd: %w", err)
	}
	conn.Write([]byte{1})
	conn.Close()
	time.Sleep(300 * time.Millisecond)

	printStats("post-exit", memfd)

	diskPath := filepath.Join(os.TempDir(), "memfd-export.bin")
	defer os.Remove(diskPath)

	dev, err := NewMemfdDevice(memfd, diskPath, int64(size), int64(chunk), workers)
	if err != nil {
		return fmt.Errorf("new device: %w", err)
	}

	// Read in reverse while export runs forward — demonstrates mixed sources.
	chunks := size / chunk
	buf := make([]byte, chunk)
	memReads, diskReads := 0, 0

	time.Sleep(50 * time.Millisecond)

	for i := chunks - 1; i >= 0; i-- {
		off := int64(i * chunk)
		fromDisk := dev.IsOnDisk(i)

		if _, err := dev.ReadAt(buf, off); err != nil {
			return fmt.Errorf("read chunk %d: %w", i, err)
		}
		if got := binary.LittleEndian.Uint64(buf[:8]); got != uint64(i) {
			return fmt.Errorf("chunk %d: expected %d, got %d", i, i, got)
		}

		if fromDisk {
			diskReads++
		} else {
			memReads++
		}
	}

	if err := dev.Wait(); err != nil {
		return fmt.Errorf("export: %w", err)
	}

	printStats("done", memfd)
	dev.Close()
	log.Printf("[consumer] all %d chunks verified (memfd=%d, disk=%d)", chunks, memReads, diskReads)
	return nil
}

func printStats(label string, fd int) {
	logical, physical, err := memfdStats(fd)
	if err != nil {
		return
	}
	log.Printf("[%-10s] logical=%6d KiB  physical=%6d KiB  (%.0f%%)",
		label, logical/1024, physical/1024, float64(physical)/float64(logical)*100)
}
