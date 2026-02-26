package compress

import (
	"fmt"
	"io"
	"runtime"
	"sort"
	"sync"
)

type entry struct {
	compOff   uint64
	compSize  uint64
	uncompOff uint64
	uncompSz  uint64
}

// writeFrames compresses frames in parallel and writes them in order.
func writeFrames(dst io.Writer, codec *zstdCodec, frameSrc func() ([]byte, error), concurrency int) ([]entry, error) {
	if concurrency <= 0 {
		concurrency = runtime.NumCPU()
	}

	type slot struct {
		uncompSz   int
		compressed []byte
		ready      bool
	}

	var (
		mu       sync.Mutex
		cond     = sync.NewCond(&mu)
		slots    []slot
		nSlots   int
		readDone bool
		firstErr error
	)

	sem := make(chan struct{}, concurrency)

	go func() {
		defer func() {
			mu.Lock()
			readDone = true
			cond.Broadcast()
			mu.Unlock()
		}()

		for i := 0; ; i++ {
			frame, err := frameSrc()
			if err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = fmt.Errorf("read frame: %w", err)
				}
				mu.Unlock()
				return
			}
			if frame == nil {
				return
			}

			idx := i
			mu.Lock()
			slots = append(slots, slot{uncompSz: len(frame)})
			nSlots = i + 1
			mu.Unlock()

			sem <- struct{}{}
			go func() {
				defer func() { <-sem }()
				c, err := codec.CompressBlock(frame)
				mu.Lock()
				if err != nil && firstErr == nil {
					firstErr = fmt.Errorf("compress frame %d: %w", idx, err)
				} else {
					slots[idx].compressed = c
				}
				slots[idx].ready = true
				cond.Broadcast()
				mu.Unlock()
			}()
		}
	}()

	var entries []entry
	var compOff, uncompOff uint64
	next := 0

	for {
		mu.Lock()
		for {
			if firstErr != nil {
				err := firstErr
				mu.Unlock()
				return nil, err
			}
			if next < nSlots && slots[next].ready {
				break
			}
			if readDone && next >= nSlots {
				mu.Unlock()
				return entries, nil
			}
			cond.Wait()
		}
		s := slots[next]
		slots[next].compressed = nil
		mu.Unlock()

		n, err := dst.Write(s.compressed)
		if err != nil {
			return nil, fmt.Errorf("write frame %d: %w", next, err)
		}

		entries = append(entries, entry{
			compOff:   compOff,
			compSize:  uint64(n),
			uncompOff: uncompOff,
			uncompSz:  uint64(s.uncompSz),
		})
		compOff += uint64(n)
		uncompOff += uint64(s.uncompSz)
		next++
	}
}

// seekableReader decompresses individual frames on demand using ReadAt.
// Thread-safe: uses io.ReaderAt (no shared cursor).
type seekableReader struct {
	src     io.ReaderAt
	codec   *zstdCodec
	entries []entry
	size    int64
}

func (r *seekableReader) Seek(offset int64, whence int) (int64, error) {
	switch whence {
	case io.SeekEnd:
		return r.size + offset, nil
	case io.SeekStart:
		return offset, nil
	default:
		return 0, fmt.Errorf("unsupported whence %d", whence)
	}
}

func (r *seekableReader) ReadAt(p []byte, off int64) (int, error) {
	if off >= r.size {
		return 0, io.EOF
	}

	n := 0
	for n < len(p) && off < r.size {
		idx := sort.Search(len(r.entries), func(i int) bool {
			e := r.entries[i]
			return int64(e.uncompOff)+int64(e.uncompSz) > off
		})
		if idx >= len(r.entries) {
			return n, io.EOF
		}

		e := r.entries[idx]
		data, err := r.decompress(e)
		if err != nil {
			return n, fmt.Errorf("frame %d: %w", idx, err)
		}

		skip := off - int64(e.uncompOff)
		copied := copy(p[n:], data[skip:])
		n += copied
		off += int64(copied)
	}

	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

func (r *seekableReader) decompress(e entry) ([]byte, error) {
	buf := make([]byte, e.compSize)
	if _, err := r.src.ReadAt(buf, int64(e.compOff)); err != nil {
		return nil, err
	}
	if e.compSize == e.uncompSz {
		return buf, nil
	}
	return r.codec.DecompressBlock(buf, int(e.uncompSz))
}
