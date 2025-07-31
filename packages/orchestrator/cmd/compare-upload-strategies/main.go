package main

import (
	"context"
	"fmt"
	"math/rand/v2"
	"time"
)

// 1.4 GB file
const bigfile = "/orchestrator/build/d0109c39-100e-4e3b-8e84-3549afb0574d-rootfs.ext4-8axqrs6axjpvbac50rf2"

const bucketName = "e2b-staging-joe-fc-templates"

const (
	runCurrentVersion    = false
	runCompressedVersion = true
	compressionAlgorithm = "lz4"
)

const (
	uploadAttempts   = 25
	downloadAttempts = 100
	maxChunksToRead  = 999 // 999 to read the whole thing
	verbose          = false
)

type mapping struct {
	index, offset, size      int64
	remoteOffset, remoteSize int64
}

type header struct {
	path  string
	items []mapping
}

func main() {
	var (
		itemMap header
		client  slabGetter
	)

	if runCurrentVersion {
		print("current process: upload ... ")
		itemMap = collectUploadStats(currentProcess)

		client = getUncompressedClient()
		collectDownloadStats(client, itemMap)
	}

	if runCompressedVersion {
		fmt.Printf("compressed process [algo = %s]: upload ... ", compressionAlgorithm)
		compressor := codecs[compressionAlgorithm]
		itemMap = collectUploadStats(func() (header, error) {
			return parallelProcess(compressor)
		})

		client = getCompressedClient(compressor)
		collectDownloadStats(client, itemMap)
	}
}

func collectUploadStats(fn func() (header, error)) header {
	var header header
	var err error

	var fastest, slowest time.Duration
	var allAttempts []time.Duration

	for range uploadAttempts {
		start := time.Now()
		header, err = fn()
		if err != nil {
			panic(err)
		}
		duration := time.Since(start)
		if fastest == 0 {
			fastest = duration
		} else {
			fastest = min(fastest, duration)
		}
		slowest = max(slowest, duration)
		allAttempts = append(allAttempts, duration)
	}

	var total int
	for _, a := range allAttempts {
		total += int(a.Milliseconds())
	}
	average := total / len(allAttempts)

	fmt.Printf(`
upload stats:
- fastest: %d ms
- slowest: %d ms
- average: %d ms
`, fastest.Milliseconds(), slowest.Milliseconds(), average)

	return header
}

func collectDownloadStats(client slabGetter, itemMap header) {
	if downloadAttempts == 0 {
		return
	}

	var fastest, slowest time.Duration
	var allAttempts []time.Duration
	for range downloadAttempts {
		// print("downloading slab ... ")
		start := time.Now()
		getRandomItem(client, itemMap)
		duration := time.Since(start)
		// println(fmt.Sprintf("%d milliseconds", duration.Milliseconds()))

		if fastest == 0 {
			fastest = duration
		} else {
			fastest = min(fastest, duration)
		}
		slowest = max(slowest, duration)
		allAttempts = append(allAttempts, duration)
	}

	var total int
	for _, a := range allAttempts {
		total += int(a.Milliseconds())
	}
	average := total / len(allAttempts)

	fmt.Printf(`
retrieval stats:
- fastest: %d ms
- slowest: %d ms
- average: %d ms
`, fastest.Milliseconds(), slowest.Milliseconds(), average)
}

type slabGetter interface {
	GetSlab(ctx context.Context, path string, item mapping) ([]byte, error)
}

func getRandomItem(client slabGetter, m header) {
	itemCount := len(m.items)
	if itemCount > 1 {
		itemCount -= 1 // last one is odd size, that's cheating
	}

	itemIndex := rand.IntN(itemCount)
	item := m.items[itemIndex]

	memory, err := client.GetSlab(context.Background(), m.path, item)
	if err != nil {
		panic(err)
	}
	if len(memory) != readChunkSize {
		panic(len(memory))
	}
}

type readRequest struct {
	index, length int64
	offset        int64
}

type compressRequest struct {
	readRequest
	data []byte
}

type uploadRequest struct {
	index          int64
	originalSize   int
	originalOffset int
	data           []byte
}

func chunks(chunkSize int64, totalSize int64) []readRequest {
	var chunks []readRequest

	var index int64
	for index = 0; index*chunkSize < totalSize && index < maxChunksToRead; index++ {
		chunks = append(chunks, readRequest{
			index:  index,
			offset: index * chunkSize,
			length: min(totalSize-index*chunkSize, chunkSize),
		})
	}

	return chunks
}
