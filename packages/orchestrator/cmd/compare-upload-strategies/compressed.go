package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/klauspost/compress/zstd"
	"github.com/pierrec/lz4/v4"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

const workerCount = 10

const (
	awsMinUploadPartSize   = 4 * 1024 * 1024
	awsMultiUploadPartSize = 10 * 1024 * 1024
	readChunkSize          = 4 * 1024 * 1024 // matches our current usage, changes here will likely lead to pain
)

var (
	compressWorkers     = runtime.NumCPU() * 2 // number of cores
	compressQueueLength = compressWorkers * 2
)

var (
	readWorkers     = compressWorkers
	readQueueLength = readWorkers * 2
)

const (
	uploadWorkers            = 10
	uploadQueueLength        = uploadWorkers * 2
	consolidationQueueLength = 20
)

func parallelProcess(codec compression) (header, error) {
	ctx := context.Background()
	objectKey := fmt.Sprintf("testing-%s", uuid.NewString())

	file, err := os.OpenFile(bigfile, os.O_RDONLY, 0o600)
	if err != nil {
		return header{}, fmt.Errorf("failed to open file: %w", err)
	}

	fileInfo, err := file.Stat()
	if err != nil {
		return header{}, fmt.Errorf("failed to stat the big file: %w", err)
	}

	var wg sync.WaitGroup
	header := header{path: objectKey}

	fileChunks := chunks(int64(readChunkSize), fileInfo.Size())
	inputCh := make(chan readRequest, readQueueLength)
	rawCh := createReader(&wg, int64(readWorkers), file, inputCh)
	consolidationQueue := createCompressor(&wg, codec, int64(compressWorkers), rawCh)
	packagesCh := createConsolidator(consolidationQueue, len(fileChunks), &header)
	if err := createUploader(ctx, &wg, objectKey, uploadWorkers, fileInfo.Size(), packagesCh); err != nil {
		return header, fmt.Errorf("failed to create uploader: %w", err)
	}

	// break apart file into blocks
	// fmt.Printf("sending %d byte file into workers ... ", fileInfo.Size())
	for _, item := range fileChunks {
		inputCh <- item
	}
	// fmt.Printf("done queueing work (%d chunks)\n", len(fileChunks))
	close(inputCh)

	// compress and upload chunks, writing to disk at the same time
	// fmt.Println("waiting for completion")
	wg.Wait()
	// fmt.Println("done waiting!")

	return header, nil
}

func (u compressed) GetSlab(ctx context.Context, path string, item mapping) ([]byte, error) {
	compressedData, err := u.uncompressed.GetSlab(ctx, path, item)
	if err != nil {
		return nil, fmt.Errorf("failed to get uncompressed data: %w", err)
	} else if int64(len(compressedData)) != item.remoteSize {
		return nil, fmt.Errorf("compressed size is wrong (%d != %d)", len(compressedData), item.remoteSize)
	}

	input := bytes.NewBuffer(compressedData)
	decompressor, err := u.codec.decompressor(input)
	if err != nil {
		return nil, fmt.Errorf("failed to create decompressor: %w", err)
	}

	output := bytes.NewBuffer(nil)
	if _, err = io.Copy(output, decompressor); err != nil {
		return nil, fmt.Errorf("failed to decompress data")
	}

	uncompressedData := output.Bytes()
	if len(uncompressedData) != int(item.size) {
		return nil, fmt.Errorf("size is wrong (%d != %d)", len(uncompressedData), item.size)
	}

	return uncompressedData, nil
}

type compressed struct {
	uncompressed
	codec compression
}

func getCompressedClient(codec compression) slabGetter {
	return &compressed{codec: codec}
}

func processChannel[TIn any](label string, count int64, work <-chan TIn, process func(item TIn) error) {
	processChannelWithOutput[TIn, struct{}](label, count, work, nil, func(item TIn) (struct{}, bool, error) {
		return struct{}{}, false, process(item)
	})
}

func processChannelWithOutput[TIn, TOut any](label string, workerCount int64, input <-chan TIn, output chan<- TOut, process func(item TIn) (TOut, bool, error)) {
	var wg sync.WaitGroup

	start := time.Now()
	var totalWorkTime, totalPublishTime, totalTime time.Duration
	var locker sync.Mutex
	var totalItemCount int64

	for index := range workerCount {
		wg.Add(1)
		go func() {
			defer wg.Done()

			start := time.Now()
			var workTime time.Duration
			var workerItemCount int64

			for item := range input {
				workerItemCount++
				t := time.Now()
				out, publish, err := process(item)
				if err != nil {
					fmt.Printf("-- %s #%d failed: %s --", label, index, err)
				}
				workTime += time.Since(t)

				if publish {
					t = time.Now()
					output <- out
					totalPublishTime += time.Since(t)
				}
			}

			locker.Lock()
			totalItemCount += workerItemCount
			totalTime += time.Since(start)
			totalWorkTime += workTime
			locker.Unlock()
		}()
	}

	wg.Wait()

	fmt.Printf("%s done in %d seconds\t\t%d workers, %d items\tavg work: %dms\tpublish: %dms\twait: %dms\n",
		label, int64(time.Since(start).Seconds()),
		workerCount, totalItemCount,
		totalWorkTime.Milliseconds()/totalItemCount,
		totalPublishTime.Milliseconds()/totalItemCount,
		(totalTime-totalWorkTime-totalPublishTime).Milliseconds()/totalItemCount,
	)
}

func createReader(wg *sync.WaitGroup, count int64, file *os.File, input <-chan readRequest) <-chan compressRequest {
	if verbose {
		println("starting readers ...")
	}

	wg.Add(1)
	output := make(chan compressRequest, compressQueueLength)

	go func() {
		processChannelWithOutput("reader", count, input, output, func(work readRequest) (compressRequest, bool, error) {
			b := make([]byte, work.length)

			read, err := file.ReadAt(b, work.offset)
			if err != nil {
				return compressRequest{}, false, fmt.Errorf("failed to read at %d: %w", work.offset, err)
			}
			if int64(read) != work.length {
				return compressRequest{}, false, fmt.Errorf("failed to complete the read: only read %d out of %d", read, work.length)
			}

			if verbose {
				fmt.Printf("read #%d: %d bytes\n", work.index, len(b))
			}
			return compressRequest{readRequest: work, data: b}, true, nil
		})

		close(output)
		wg.Done()
	}()

	return output
}

func createCompressor(wg *sync.WaitGroup, codec compression, count int64, input <-chan compressRequest) <-chan uploadRequest {
	if verbose {
		println("starting compressors ...")
	}

	wg.Add(1)
	output := make(chan uploadRequest, consolidationQueueLength)

	go func() {
		defer wg.Done()

		processChannelWithOutput("compressor", count, input, output, func(work compressRequest) (uploadRequest, bool, error) {
			compressed := bytes.NewBuffer(nil)
			compressor, err := codec.compressor(compressed)
			if err != nil {
				return uploadRequest{}, false, fmt.Errorf("faield to create compressor: %w", err)
			}
			_, err = compressor.Write(work.data)
			if err != nil {
				return uploadRequest{}, false, fmt.Errorf("failed to compress block: %w", err)
			}
			if err = compressor.Close(); err != nil {
				return uploadRequest{}, false, fmt.Errorf("failed to close compressor: %w", err)
			}
			data := compressed.Bytes()
			if verbose {
				fmt.Printf("compressed #%d: %d to %d\n", work.index, len(work.data), len(data))
			}

			return uploadRequest{
				index:        work.index,
				originalSize: len(work.data),
				data:         data,
			}, true, nil
		})

		close(output)
	}()

	return output
}

func createConsolidator(input <-chan uploadRequest, totalParts int, header *header) <-chan uploadRequest {
	if verbose {
		println("starting consolidator ...")
	}

	output := make(chan uploadRequest, uploadQueueLength)

	go func() {
		var (
			holding          = make(map[int64]uploadRequest)
			currentIndex     int64
			nextUploadIndex  int64
			totalBytesSent   = 0
			compressedOffset = 0
		)

		for item := range input {
			// store the new chunk
			if verbose {
				fmt.Printf("consolidator: received #%d (%d bytes)\n", item.index, len(item.data))
			}
			holding[item.index] = item

			// see if we should send a packet
			firstIndex := currentIndex
			lastIndex := firstIndex
			var packetIndexes []int64
			var packetSize int

			for {
				item, ok := holding[lastIndex]
				if !ok {
					if verbose {
						fmt.Printf("- only have %d bytes, but haven't received #%d yet\n", packetSize, lastIndex)
					}
					break
				}

				if verbose {
					fmt.Printf("- appending #%d (%d bytes) to packet\n", item.index, len(item.data))
				}
				packetIndexes = append(packetIndexes, item.index)
				packetSize += len(item.data)
				if lastIndex+1 == int64(totalParts) { // this is the last item
					if packetSize < awsMinUploadPartSize {
						panic("we screwed up! the last packet is too small!")
					}
				} else if packetSize < awsMultiUploadPartSize {
					lastIndex++
					continue
				}

				buffer := make([]byte, 0, packetSize)
				for _, index := range packetIndexes {
					packet, ok := holding[index]
					if !ok {
						panic("wtf is going on here??")
					}
					buffer = append(buffer, packet.data...)
				}
				if verbose {
					fmt.Printf("consolidator: submitting chunk %d, %d bytes, with %d-%d", nextUploadIndex, len(buffer), firstIndex, lastIndex)
				}
				output <- uploadRequest{
					index: nextUploadIndex,
					data:  buffer,
				}

				// record the work done
				totalBytesSent += len(buffer)
				nextUploadIndex++
				currentIndex = lastIndex + 1
				for i := firstIndex; i <= lastIndex; i++ {
					item = holding[i]
					header.items = append(header.items, mapping{
						index:        item.index,
						offset:       int64(item.originalOffset),
						size:         int64(item.originalSize),
						remoteSize:   int64(len(item.data)),
						remoteOffset: int64(compressedOffset),
					})
					compressedOffset += len(item.data)

					delete(holding, i)
				}

				// reset to loop again
				firstIndex = currentIndex
				lastIndex = firstIndex
				packetIndexes = []int64{}
				packetSize = 0
			}

			if totalBytesSent == totalParts {
				break
			}
		}

		if len(holding) != 0 {
			panic("we have leftover data!")
		}

		close(output)
	}()

	return output
}

func createUploader(ctx context.Context, wg *sync.WaitGroup, objectKey string, count, fileSize int64, input <-chan uploadRequest) error {
	if verbose {
		println("starting uploader ...")
	}

	uploader, err := storage.NewMultipartUploaderWithRetryConfig(
		ctx, bucketName, objectKey,
		storage.DefaultRetryConfig())
	if err != nil {
		return fmt.Errorf("failed to create uploader: %w", err)
	}

	uploadID, err := uploader.InitiateUpload()
	if err != nil {
		return fmt.Errorf("failed to initiate upload: %w", err)
	}

	wg.Add(1)

	go func() {
		defer wg.Done()

		var m sync.Map

		var total atomic.Int64

		processChannel("uploader", count, input, func(work uploadRequest) error {
			if verbose {
				fmt.Printf("upload #%d\n", work.index)
			}
			total.Add(int64(len(work.data)))

			response, err := uploader.UploadPart(uploadID, int(work.index+1), work.data)
			if err != nil {
				return fmt.Errorf("failed to upload part #%d: %w", work.index, err)
			}

			m.Store(work.index, response)

			return nil
		})

		var parts []storage.Part
		m.Range(func(key, value any) bool {
			index := key.(int64)
			etag := value.(string)
			parts = append(parts, storage.Part{PartNumber: int(index + 1), ETag: etag})
			return true
		})
		slices.SortFunc(parts, func(a, b storage.Part) int {
			return a.PartNumber - b.PartNumber
		})
		compressedSize := total.Load()
		fmt.Printf("completing upload of %d parts (%d bytes -> %d bytes, %d%% of original size) ... ",
			len(parts), fileSize, compressedSize, int(float64(compressedSize)/float64(fileSize)*100))
		if err := uploader.CompleteUpload(uploadID, parts); err != nil {
			panic(fmt.Errorf("failed to complete upload: %w", err))
		}
	}()

	return nil
}

type compression struct {
	compressor   func(w io.Writer) (io.WriteCloser, error)
	decompressor func(r io.Reader) (io.Reader, error)
}

var codecs = map[string]compression{
	"gzip": {
		compressor: func(w io.Writer) (io.WriteCloser, error) {
			return gzip.NewWriter(w), nil
		},
		decompressor: func(r io.Reader) (io.Reader, error) {
			return gzip.NewReader(r)
		},
	},
	"lz4": {
		compressor: func(w io.Writer) (io.WriteCloser, error) {
			c := lz4.NewWriter(w)
			if err := c.Apply(
				lz4.CompressionLevelOption(lz4.Fast),
				lz4.BlockSizeOption(lz4.Block256Kb),
				lz4.ChecksumOption(false),
				lz4.BlockChecksumOption(false),
				lz4.ConcurrencyOption(1),
			); err != nil {
				return nil, fmt.Errorf("failed to set options: %w", err)
			}
			return c, nil
		},
		decompressor: func(r io.Reader) (io.Reader, error) {
			return lz4.NewReader(r), nil
		},
	},
	"zstd": {
		compressor: func(w io.Writer) (io.WriteCloser, error) {
			return zstd.NewWriter(w, zstd.WithEncoderConcurrency(1))
		},
		decompressor: func(r io.Reader) (io.Reader, error) {
			return zstd.NewReader(r, zstd.WithDecoderConcurrency(1))
		},
	},
}
