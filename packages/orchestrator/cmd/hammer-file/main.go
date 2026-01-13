package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"sort"
	"sync"
	"time"

	"cloud.google.com/go/storage"
	"golang.org/x/sync/errgroup"
	"google.golang.org/api/option"
	"google.golang.org/grpc"
)

const (
	megabyte  = 1024 * 1024
	chunkSize = 4 * megabyte // 4MB
)

type readTiming struct {
	scenario string
	startMs  int64
	endMs    int64
}

func main() {
	var bucketName, objectName string
	flag.StringVar(&bucketName, "bucket", "", "GCS bucket name")
	flag.StringVar(&objectName, "object", "", "GCS object name")
	flag.Parse()

	if bucketName == "" || objectName == "" {
		fmt.Println("Usage: hammer-file -bucket <bucket> -object <object>")
		os.Exit(1)
	}

	ctx := context.Background()
	client, err := storage.NewGRPCClient(ctx,
		option.WithGRPCConnectionPool(4),
		option.WithGRPCDialOption(grpc.WithInitialConnWindowSize(32*megabyte)),
		option.WithGRPCDialOption(grpc.WithInitialWindowSize(4*megabyte)),
	)
	if err != nil {
		log.Fatalf("Failed to create client: %v", err)
	}
	defer client.Close()

	bucket := client.Bucket(bucketName)
	obj := bucket.Object(objectName)

	attrs, err := obj.Attrs(ctx)
	if err != nil {
		log.Fatalf("Failed to get object attributes: %v", err)
	}
	fileSize := attrs.Size
	fmt.Printf("File size: %d bytes (%.2f MB)\n", fileSize, float64(fileSize)/(1024*1024))

	var seqTimings []readTiming
	var parTimings []readTiming
	firstRequestStart := time.Time{}

	// Scenario 1: Sequential range reads
	fmt.Println("\nScenario 1: Sequential range reads...")
	seqStart := time.Now()
	seqChunkDurations := make([]time.Duration, 0)
	for offset := int64(0); offset < fileSize; offset += chunkSize {
		length := int64(chunkSize)
		if offset+length > fileSize {
			length = fileSize - offset
		}

		start := time.Now()
		if firstRequestStart.IsZero() {
			firstRequestStart = start
		}

		err := readRange(ctx, obj, offset, length)
		duration := time.Since(start)
		if err != nil {
			log.Fatalf("Sequential read failed at offset %d: %v", offset, err)
		}
		seqChunkDurations = append(seqChunkDurations, duration)
		seqTimings = append(seqTimings, readTiming{
			scenario: "Sequential",
			startMs:  start.Sub(firstRequestStart).Milliseconds(),
			endMs:    time.Now().Sub(firstRequestStart).Milliseconds(),
		})
	}
	seqTotalDuration := time.Since(seqStart)

	// Results
	fmt.Println("\n--- Results ---")
	fmt.Printf("%-20s %-15s %-15s %-15s\n", "Scenario", "Mean Chunk", "P50 Chunk", "Total Time")

	seqStats := getStats(seqChunkDurations)
	fmt.Printf("%-20s %-15s %-15s %-15s %-15d\n", "Sequential", seqStats.mean, seqStats.p50, seqTotalDuration.Round(time.Millisecond), seqStats.count)

	// Scenario 2: Parallel range reads
	fmt.Printf("\nScenario 2: Parallel range reads...\n")
	for _, concurrency := range []int{10} {
		//for concurrency := 2; concurrency <= 15; concurrency++ {
		parStart := time.Now()
		parChunkDurations := make([]time.Duration, 0)
		var mu sync.Mutex

		g, gCtx := errgroup.WithContext(ctx)
		g.SetLimit(concurrency)
		for chunk := 0; chunk < int(fileSize/chunkSize); chunk++ {
			offset := int64(chunk) * chunkSize
			g.Go(func() error {
				length := min(chunkSize, fileSize-offset)
				//println(fmt.Sprintf("Reading chunk [%d] (start)", chunk))
				start := time.Now()
				mu.Lock()
				if firstRequestStart.IsZero() {
					firstRequestStart = start
				}
				mu.Unlock()

				err := readRange(gCtx, obj, offset, length)
				duration := time.Since(start)
				// println(fmt.Sprintf("Reading chunk [%d] (done) [duration = %s]", chunk, duration.Round(time.Millisecond)))
				if err != nil {
					return err
				}
				mu.Lock()
				parChunkDurations = append(parChunkDurations, duration)
				parTimings = append(parTimings, readTiming{
					scenario: fmt.Sprintf("Parallel-%d", concurrency),
					startMs:  start.Sub(firstRequestStart).Milliseconds(),
					endMs:    time.Now().Sub(firstRequestStart).Milliseconds(),
				})
				mu.Unlock()
				return nil
			})
		}
		if err := g.Wait(); err != nil {
			log.Fatalf("Parallel reads failed (concurrency %d): %v", concurrency, err)
		}
		parTotalDuration := time.Since(parStart)

		parStats := getStats(parChunkDurations)
		fmt.Printf("%-20s %-15s %-15s %-15s %-15d\n", fmt.Sprintf("Parallel (%d)", concurrency), parStats.mean, parStats.p50, parTotalDuration.Round(time.Millisecond), parStats.count)
	}

	writeMermaidGantt("scenario1.mmd", seqTimings)
	writeMermaidGantt("scenario2.mmd", parTimings)
}

func writeMermaidGantt(filename string, timings []readTiming) {
	f, err := os.Create(filename)
	if err != nil {
		log.Fatalf("Failed to create Gantt file %s: %v", filename, err)
	}
	defer f.Close()

	fmt.Fprintln(f, "gantt")
	fmt.Fprintln(f, "    title GCS Read Performance")
	fmt.Fprintln(f, "    dateFormat x")
	fmt.Fprintln(f, "    axisFormat %L")

	currentScenario := ""
	for i, t := range timings {
		if t.scenario != currentScenario {
			fmt.Fprintf(f, "    section %s\n", t.scenario)
			currentScenario = t.scenario
		}
		fmt.Fprintf(f, "    Chunk %d : %d, %d\n", i, t.startMs, t.endMs)
	}
}

func readRange(ctx context.Context, obj *storage.ObjectHandle, offset, length int64) error {
	r, err := obj.NewRangeReader(ctx, offset, length)
	if err != nil {
		return err
	}
	defer r.Close()

	_, err = io.Copy(io.Discard, r)
	return err
}

type stats struct {
	count int
	mean  time.Duration
	p50   time.Duration
}

func getStats(durations []time.Duration) stats {
	if len(durations) == 0 {
		return stats{}
	}

	var total time.Duration
	for _, d := range durations {
		total += d
	}
	mean := total / time.Duration(len(durations))

	sort.Slice(durations, func(i, j int) bool {
		return durations[i] < durations[j]
	})
	p50 := durations[len(durations)/2]

	return stats{
		count: len(durations),
		mean:  mean.Round(time.Millisecond),
		p50:   p50.Round(time.Millisecond),
	}
}
