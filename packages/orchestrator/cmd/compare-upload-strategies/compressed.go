package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/google/uuid"
	"io"
	"os"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"
	"time"
)

const workerCount = 10

const awsMinUploadPartSize = 4 * 1024 * 1024
const awsMultiUploadPartSize = 10 * 1024 * 1024
const readChunkSize = 4 * 1024 * 1024 // matches our current usage, changes here will likely lead to pain

var compressWorkers = runtime.NumCPU() * 2 // number of cores
var compressQueueLength = compressWorkers * 2

var readWorkers = compressWorkers
var readQueueLength = readWorkers * 2

const uploadWorkers = 10
const uploadQueueLength = uploadWorkers * 2
const consolidationQueueLength = 20

func (u compressed) GetSlab(ctx context.Context, path string, item mapping) ([]byte, error) {
	compressedData, err := u.uncompressed.GetSlab(ctx, path, item)
	if err != nil {
		return nil, fmt.Errorf("failed to get uncompressed data: %w", err)
	} else if int64(len(compressedData)) != item.remoteSize {
		return nil, fmt.Errorf("compressed size is wrong (%d != %d)", len(compressedData), item.remoteSize)
	}

	input := bytes.NewBuffer(compressedData)
	decompressor, err := gzip.NewReader(input)
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
}

func getCompressedClient() slabGetter {
	return &compressed{}
}

func processChannel[T any](label string, count int64, work <-chan T, process func(item T) error) {
	var wg sync.WaitGroup

	start := time.Now()
	var totalWorkTime, totalTime time.Duration
	var locker sync.Mutex

	for index := range count {
		wg.Add(1)
		go func() {
			defer wg.Done()

			start := time.Now()
			var workTime time.Duration

			for item := range work {
				t := time.Now()
				if err := process(item); err != nil {
					fmt.Printf("-- %s #%d failed: %s --", label, index, err)
				}
				workTime += time.Since(t)
			}

			locker.Lock()
			totalTime += time.Since(start)
			totalWorkTime += workTime
			locker.Unlock()
		}()
	}

	wg.Wait()

	fmt.Printf("%s done in %d seconds\t\twork: %dms\twait: %dms\n",
		label, int64(time.Since(start).Seconds()),
		totalWorkTime.Milliseconds(),
		(totalTime - totalWorkTime).Milliseconds(),
	)
}

func parallelProcess() (header, error) {
	ctx := context.Background()
	objectKey := fmt.Sprintf("testing-%s", uuid.NewString())

	file, err := os.OpenFile(bigfile, os.O_RDONLY, 0600)
	if err != nil {
		return header{}, fmt.Errorf("failed to open file: %w", err)
	}

	fileInfo, err := file.Stat()
	if err != nil {
		return header{}, fmt.Errorf("failed to stat the big file: %w", err)
	}

	uploader, err := storage.NewMultipartUploaderWithRetryConfig(
		ctx, bucketName, objectKey,
		storage.DefaultRetryConfig())
	if err != nil {
		return header{}, fmt.Errorf("failed to create uploader: %w", err)
	}

	uploadID, err := uploader.InitiateUpload()
	if err != nil {
		return header{}, fmt.Errorf("failed to initiate upload: %w", err)
	}

	var wg sync.WaitGroup

	createReader := func(count int64, file *os.File, input <-chan readRequest) <-chan compressRequest {
		if verbose {
			println("starting readers ...")
		}

		wg.Add(1)
		output := make(chan compressRequest, compressQueueLength)

		go func() {
			processChannel("reader", count, input, func(work readRequest) error {
				b := make([]byte, work.length)

				read, err := file.ReadAt(b, work.offset)
				if err != nil {
					return fmt.Errorf("failed to read at %d: %w", work.offset, err)
				}
				if int64(read) != work.length {
					return fmt.Errorf("failed to complete the read: only read %d out of %d", read, work.length)
				}

				if verbose {
					fmt.Printf("read #%d: %d bytes\n", work.index, len(b))
				}
				output <- compressRequest{readRequest: work, data: b}
				return nil
			})

			close(output)
			wg.Done()
		}()

		return output
	}

	createCompressor := func(count int64, input <-chan compressRequest) <-chan uploadRequest {
		if verbose {
			println("starting compressors ...")
		}

		wg.Add(1)
		output := make(chan uploadRequest, consolidationQueueLength)

		go func() {
			defer wg.Done()

			processChannel("compressor", count, input, func(work compressRequest) error {
				compressed := bytes.NewBuffer(nil)
				writer := gzip.NewWriter(compressed)
				_, err = writer.Write(work.data)
				if err != nil {
					return fmt.Errorf("failed to compress block: %w", err)
				}
				if err = writer.Close(); err != nil {
					return fmt.Errorf("failed to close compressor: %w", err)
				}
				data := compressed.Bytes()
				if verbose {
					fmt.Printf("compressed #%d: %d to %d\n", work.index, len(work.data), len(data))
				}
				output <- uploadRequest{
					index:        work.index,
					originalSize: len(work.data),
					data:         data,
				}

				return nil
			})

			close(output)
		}()

		return output
	}

	var maps []mapping

	createConsolidator := func(input <-chan uploadRequest, totalParts int) <-chan uploadRequest {
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
						maps = append(maps, mapping{
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

	createUploader := func(count, fileSize int64, input <-chan uploadRequest) {
		if verbose {
			println("starting uploader ...")
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
			fmt.Printf("completing upload of %d parts (%d bytes -> %d bytes) ... ", len(parts), fileSize, total.Load())
			if err = uploader.CompleteUpload(uploadID, parts); err != nil {
				panic(fmt.Errorf("failed to complete upload: %w", err))
			}
		}()
	}

	fileChunks := chunks(int64(readChunkSize), fileInfo.Size())
	inputCh := make(chan readRequest, readQueueLength)
	rawCh := createReader(int64(readWorkers), file, inputCh)
	consolidationQueue := createCompressor(int64(compressWorkers), rawCh)
	packagesCh := createConsolidator(consolidationQueue, len(fileChunks))
	createUploader(uploadWorkers, fileInfo.Size(), packagesCh)

	// break apart file into blocks
	fmt.Printf("sending %d byte file into workers ... ", fileInfo.Size())
	for _, item := range fileChunks {
		inputCh <- item
	}
	fmt.Printf("done queueing work (%d chunks)\n", len(fileChunks))
	close(inputCh)

	// compress and upload chunks, writing to disk at the same time
	println("waiting for completion")
	wg.Wait()
	println("done waiting!")

	return header{
		path:  objectKey,
		items: maps,
	}, nil
}
