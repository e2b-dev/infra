package main

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"slices"
	"time"

	"cloud.google.com/go/storage"
	"google.golang.org/api/iterator"
)

func (p *processor) findFiles() error {
	ctx := context.Background()
	client, err := storage.NewClient(ctx)
	if err != nil {
		return fmt.Errorf("failed to create storage client: %w", err)
	}
	defer client.Close()

	var paths []fileInfo

	it := client.Bucket(p.bucket).Objects(ctx, nil)
	for {
		attrs, err := it.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return fmt.Errorf("failed to list objects in bucket %q: %w", p.bucket, err)
		}

		if attrs.Size < p.minFileSize {
			continue
		}

		paths = append(paths, fileInfo{path: attrs.Name, size: attrs.Size})

		if p.limitFileCount > 0 && len(paths) >= p.limitFileCount {
			break
		}
	}

	p.allFiles = paths

	fmt.Printf("found %d files\n", len(p.allFiles))

	return nil
}

func removeAtIndex[T any](items []T, idx int) []T {
	return slices.Delete(items, idx, idx+1)
}

type files struct {
	rand       *rand.Rand
	paths      []fileInfo
	chunkSize  int64
	usedRanges map[string]map[int64]struct{}
}

func newFiles(paths []fileInfo, chunkSize int64) *files {
	return &files{
		rand:       rand.New(rand.NewSource(time.Now().UnixNano())),
		paths:      paths,
		chunkSize:  chunkSize,
		usedRanges: make(map[string]map[int64]struct{}),
	}
}

func (f *files) nextRead() (string, int64, error) {
	if len(f.paths) == 0 {
		return "", 0, fmt.Errorf("no files found")
	}

	idx := f.rand.Intn(len(f.paths))
	info := f.paths[idx]

	totalChunks := info.size / f.chunkSize
	for {
		offset := f.rand.Int63n(totalChunks - 1) // the last one might not be full, just skip it
		usedOffsets, isFileUsed := f.usedRanges[info.path]
		if !isFileUsed {
			f.usedRanges[info.path] = map[int64]struct{}{
				offset: {},
			}

			return info.path, offset, nil
		}

		if _, used := usedOffsets[offset]; !used {
			usedOffsets[offset] = struct{}{}

			return info.path, offset, nil
		}
	}
}
