package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"slices"
	"sync"
	"sync/atomic"

	"golang.org/x/sync/errgroup"
)

type partUploader interface {
	Start(ctx context.Context) error
	UploadPart(ctx context.Context, partIndex int, data ...[]byte) error
	Complete(ctx context.Context) error
	Close() error
}

type memPartUploader struct {
	mu    sync.Mutex
	parts map[int][]byte
}

func (m *memPartUploader) Start(context.Context) error {
	m.parts = make(map[int][]byte)

	return nil
}

func (m *memPartUploader) UploadPart(_ context.Context, partIndex int, data ...[]byte) error {
	var buf bytes.Buffer
	for _, d := range data {
		buf.Write(d)
	}
	m.mu.Lock()
	m.parts[partIndex] = buf.Bytes()
	m.mu.Unlock()

	return nil
}

func (m *memPartUploader) Complete(context.Context) error { return nil }
func (m *memPartUploader) Close() error                   { return nil }

func (m *memPartUploader) Assemble() []byte {
	keys := make([]int, 0, len(m.parts))
	for k := range m.parts {
		keys = append(keys, k)
	}
	slices.Sort(keys)

	var buf bytes.Buffer
	for _, k := range keys {
		buf.Write(m.parts[k])
	}

	return buf.Bytes()
}

type frame struct {
	uncompressedSize int
	compressed       []byte
}

type part struct {
	index          int
	frames         []*frame
	compressedSize atomic.Int64
	compress       *errgroup.Group
}

func newPart(index int, parentCtx context.Context, workers int) (*part, context.Context) {
	p := &part{index: index}
	var ctx context.Context
	p.compress, ctx = errgroup.WithContext(parentCtx)
	p.compress.SetLimit(workers)

	return p, ctx
}

func (p *part) addFrame(ctx context.Context, uncompressedData []byte, pool *sync.Pool) {
	frameInPart := &frame{uncompressedSize: len(uncompressedData)}
	p.frames = append(p.frames, frameInPart)

	p.compress.Go(func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		c := pool.Get().(compressor)
		out, err := c.compress(uncompressedData)
		pool.Put(c)
		if err != nil {
			return err
		}
		frameInPart.compressed = out
		p.compressedSize.Add(int64(len(out)))

		return nil
	})
}

func compressStream(ctx context.Context, in io.Reader, cfg CompressConfig, uploader partUploader, maxUploadConcurrency int) (*FrameTable, [32]byte, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	if err := uploader.Start(ctx); err != nil {
		return nil, [32]byte{}, fmt.Errorf("start upload: %w", err)
	}
	defer uploader.Close()

	// The read loop goroutine holds one slot for the duration of the stream;
	// at least one additional slot is required for uploaders to make progress.
	if maxUploadConcurrency < 1 {
		maxUploadConcurrency = 1
	}
	work, workCtx := errgroup.WithContext(ctx)
	work.SetLimit(maxUploadConcurrency + 1)

	// Start the read loop.
	q := make(chan *part, 2)
	hasher := sha256.New()
	work.Go(func() error {
		defer close(q)

		return readLoop(workCtx, in, cfg, hasher, q)
	})

	// Upload loop.
	var frameSizes []FrameSize
	var loopErr error
	for p := range q {
		if err := p.compress.Wait(); err != nil {
			loopErr = fmt.Errorf("compress frames: %w", err)
			cancel()

			break
		}

		var compressed [][]byte
		for _, f := range p.frames {
			frameSizes = append(frameSizes, FrameSize{U: int32(f.uncompressedSize), C: int32(len(f.compressed))})
			compressed = append(compressed, f.compressed)
		}

		pi := p.index
		work.Go(func() error {
			return uploader.UploadPart(workCtx, pi, compressed...)
		})
	}

	// Drain q so the read loop can exit and close it, then wait for all
	// in-flight uploads to finish before the deferred uploader.Close().
	for range q { //nolint:revive // intentional drain
	}
	workErr := work.Wait()

	if err := errors.Join(loopErr, workErr); err != nil {
		return nil, [32]byte{}, err
	}

	if err := uploader.Complete(ctx); err != nil {
		return nil, [32]byte{}, fmt.Errorf("complete upload: %w", err)
	}

	var checksum [32]byte
	copy(checksum[:], hasher.Sum(nil))
	ft := NewFrameTable(cfg.CompressionType(), frameSizes)

	return ft, checksum, nil
}

func readLoop(ctx context.Context, in io.Reader, cfg CompressConfig, hasher io.Writer, q chan<- *part) error {
	compressors, err := newCompressorPool(cfg)
	if err != nil {
		return err
	}

	frameSize := cfg.FrameSize()
	minPartSize := cfg.MinPartSize()
	workers := max(cfg.FrameEncodeWorkers, 1)
	p, compressCtx := newPart(1, ctx, workers)

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		buf := make([]byte, frameSize)
		n, err := io.ReadFull(in, buf)

		eof := errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)
		if err != nil && !eof {
			return fmt.Errorf("read frame: %w", err)
		}

		if n > 0 {
			hasher.Write(buf[:n])
			p.addFrame(compressCtx, buf[:n], compressors)
		}

		if eof {
			if len(p.frames) > 0 {
				select {
				case q <- p:
				case <-ctx.Done():
					return ctx.Err()
				}
			}

			return nil
		}

		if p.compressedSize.Load() >= minPartSize {
			select {
			case q <- p:
			case <-ctx.Done():
				return ctx.Err()
			}

			p, compressCtx = newPart(p.index+1, ctx, workers)
		}
	}
}
