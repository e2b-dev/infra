package storage

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/klauspost/compress/zstd"
)

// Benchmark for zstd decoder concurrency tuning. Compares default
// (GOMAXPROCS internal goroutines per decoder) against
// zstd.WithDecoderConcurrency(1) under both sequential and parallel
// decode patterns. The parallel pattern mirrors production: many
// concurrent resumes each pulling decoders from a shared sync.Pool.
//
// Realistic source data: any binary or library at the candidate
// paths (Go binaries / libc give ~3× compression ratio, similar to
// memfile snapshots). The benchmark is skipped on systems where
// none of the candidate sources exist or are large enough.
//
// Frame size is 2 MiB to match DefaultCompressFrameSize in
// compress_config.go and the actual frame size used in production.

const (
	benchChunkSize   = 2 << 20 // 2 MiB — matches DefaultCompressFrameSize
	benchMinChunks   = 20
	benchTotalNeeded = benchChunkSize * benchMinChunks
)

var benchSourceCandidates = [][]string{
	{"/home/lev/dev/infra/packages/orchestrator/orchestrator"},
	{"/home/lev/go/bin/golangci-lint"},
	{
		"/usr/lib/x86_64-linux-gnu/libc.so.6",
		"/usr/lib/x86_64-linux-gnu/libstdc++.so.6",
		"/usr/lib/x86_64-linux-gnu/libcrypto.so.3",
	},
}

type benchChunkSet struct {
	sources    []string
	compressed [][]byte
	rawTotal   int64
	compTotal  int64
	minComp    int
	maxComp    int
}

var (
	benchLoadOnce sync.Once
	benchLoaded   *benchChunkSet
	benchLoadErr  error
)

func loadBenchChunks() (*benchChunkSet, error) {
	benchLoadOnce.Do(func() {
		var raw []byte
		var used []string
		for _, group := range benchSourceCandidates {
			raw = raw[:0]
			used = used[:0]
			for _, p := range group {
				b, err := os.ReadFile(p)
				if err != nil {
					continue
				}
				raw = append(raw, b...)
				used = append(used, p)
				if len(raw) >= benchTotalNeeded {
					break
				}
			}
			if len(raw) >= benchTotalNeeded {
				break
			}
		}
		if len(raw) < benchTotalNeeded {
			benchLoadErr = fmt.Errorf("no source data: need %d bytes from candidates, got %d", benchTotalNeeded, len(raw))
			return
		}

		set := &benchChunkSet{
			sources: used,
			minComp: int(^uint(0) >> 1),
		}
		for off := 0; off+benchChunkSize <= len(raw) && len(set.compressed) < benchMinChunks*2; off += benchChunkSize {
			plain := raw[off : off+benchChunkSize]
			var buf bytes.Buffer
			w, err := zstd.NewWriter(&buf)
			if err != nil {
				benchLoadErr = err
				return
			}
			if _, err := w.Write(plain); err != nil {
				benchLoadErr = err
				return
			}
			if err := w.Close(); err != nil {
				benchLoadErr = err
				return
			}
			c := buf.Bytes()
			set.compressed = append(set.compressed, c)
			set.rawTotal += int64(len(plain))
			set.compTotal += int64(len(c))
			if len(c) < set.minComp {
				set.minComp = len(c)
			}
			if len(c) > set.maxComp {
				set.maxComp = len(c)
			}
		}
		if len(set.compressed) < benchMinChunks {
			benchLoadErr = fmt.Errorf("only produced %d chunks, need %d", len(set.compressed), benchMinChunks)
			return
		}
		benchLoaded = set
	})
	return benchLoaded, benchLoadErr
}

// benchDecoderPool mirrors production's getZstdDecoder pattern:
// create-once via sync.Pool New, reuse-many via Reset.
type benchDecoderPool struct {
	pool sync.Pool
}

func newBenchDecoderPool(opts ...zstd.DOption) *benchDecoderPool {
	return &benchDecoderPool{
		pool: sync.Pool{
			New: func() any {
				d, err := zstd.NewReader(nil, opts...)
				if err != nil {
					panic(err)
				}
				return d
			},
		},
	}
}

func (p *benchDecoderPool) get() *zstd.Decoder  { return p.pool.Get().(*zstd.Decoder) }
func (p *benchDecoderPool) put(d *zstd.Decoder) { p.pool.Put(d) }

func benchSetup(b *testing.B) *benchChunkSet {
	cs, err := loadBenchChunks()
	if err != nil {
		b.Skipf("zstd decoder benchmark skipped: %v", err)
	}
	return cs
}

func runSequential(b *testing.B, opts ...zstd.DOption) {
	cs := benchSetup(b)
	pool := newBenchDecoderPool(opts...)
	b.SetBytes(int64(benchChunkSize))
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		c := cs.compressed[i%len(cs.compressed)]
		dec := pool.get()
		if err := dec.Reset(bytes.NewReader(c)); err != nil {
			b.Fatal(err)
		}
		n, err := io.Copy(io.Discard, dec)
		if err != nil {
			b.Fatal(err)
		}
		if n != int64(benchChunkSize) {
			b.Fatalf("decoded %d, want %d", n, benchChunkSize)
		}
		pool.put(dec)
	}
}

func runParallel(b *testing.B, opts ...zstd.DOption) {
	cs := benchSetup(b)
	pool := newBenchDecoderPool(opts...)
	b.SetBytes(int64(benchChunkSize))
	b.ReportAllocs()
	var counter atomic.Uint64
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			idx := counter.Add(1)
			c := cs.compressed[int(idx)%len(cs.compressed)]
			dec := pool.get()
			if err := dec.Reset(bytes.NewReader(c)); err != nil {
				b.Fatal(err)
			}
			n, err := io.Copy(io.Discard, dec)
			if err != nil {
				b.Fatal(err)
			}
			if n != int64(benchChunkSize) {
				b.Fatalf("decoded %d, want %d", n, benchChunkSize)
			}
			pool.put(dec)
		}
	})
}

func BenchmarkZstdDecoderDefault(b *testing.B)      { runSequential(b) }
func BenchmarkZstdDecoderConcurrency1(b *testing.B) { runSequential(b, zstd.WithDecoderConcurrency(1)) }
func BenchmarkZstdDecoderDefault_Parallel(b *testing.B) {
	runParallel(b)
}

func BenchmarkZstdDecoderConcurrency1_Parallel(b *testing.B) {
	runParallel(b, zstd.WithDecoderConcurrency(1))
}
