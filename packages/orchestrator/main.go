package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	_ "net/http/pprof" // This will register pprof handlers
	"os"
	"os/signal"
	"runtime"
	"runtime/pprof"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/server"
	"github.com/e2b-dev/infra/packages/shared/pkg/env"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const defaultPort = 5008

func startMemoryProfiling() {
	// Create a ticker to collect memory profiles periodically
	ticker := time.NewTicker(15 * time.Minute)
	go func() {
		count := 0
		for range ticker.C {
			filename := fmt.Sprintf("/tmp/memprofile-%d.pprof", count)
			f, err := os.Create(filename)
			if err != nil {
				log.Printf("Could not create memory profile: %v", err)
				continue
			}
			defer f.Close()

			// Force a garbage collection before taking the profile
			runtime.GC()
			if err := pprof.WriteHeapProfile(f); err != nil {
				log.Printf("Could not write memory profile: %v", err)
			}
			log.Printf("Memory profile written to %s", filename)
			count++
		}
	}()
}

func monitorMemoryUsage() {
	go func() {
		var m runtime.MemStats
		for {
			runtime.ReadMemStats(&m)
			log.Printf("Alloc = %v MiB", m.Alloc/1024/1024)
			log.Printf("TotalAlloc = %v MiB", m.TotalAlloc/1024/1024)
			log.Printf("Sys = %v MiB", m.Sys/1024/1024)
			log.Printf("NumGC = %v", m.NumGC)
			time.Sleep(1 * time.Minute)
		}
	}()
}

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		log.Println("Starting pprof server on :6060")
		log.Println(http.ListenAndServe("localhost:6060", nil))
	}()

	go startMemoryProfiling()
	go monitorMemoryUsage()

	sig, sigCancel := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer sigCancel()

	var port uint

	flag.UintVar(&port, "port", defaultPort, "orchestrator server port")
	flag.Parse()

	wg := &sync.WaitGroup{}
	exitCode := &atomic.Int32{}
	telemetrySignal := make(chan struct{})

	// defer waiting on the waitgroup so that this runs even when
	// there's a panic.
	defer wg.Wait()

	if !env.IsLocal() {
		shutdown := telemetry.InitOTLPExporter(ctx, server.ServiceName, "no")
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-telemetrySignal
			if err := shutdown(ctx); err != nil {
				log.Printf("telemetry shutdown: %v", err)
				exitCode.Add(1)
			}
		}()
	}

	srv, err := server.New(ctx, port)
	if err != nil {
		log.Fatalf("failed to create server: %v", err)
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		var err error

		defer func() {
			// recover the panic because the service manages a number of go routines
			// that can panic, so catching this here allows for the rest of the process
			// to terminate in a more orderly manner.
			if perr := recover(); perr != nil {
				// many of the panics use log.Panicf which means we're going to log
				// some panic messages twice, but this seems ok, and temporary while
				// we clean up logging.
				log.Printf("caught panic in service: %v", perr)
				exitCode.Add(1)
				err = errors.Join(err, fmt.Errorf("server panic: %v", perr))
			}

			// if we encountered an err, but the signal context was NOT canceled, then
			// the outer context needs to be canceled so the remainder of the service
			// can shutdown.
			if err != nil && sig.Err() == nil {
				log.Printf("service ended early without signal")
				cancel()
			}
		}()

		// this sets the error declared above so the function
		// in the defer can check it.
		if err = srv.Start(ctx); err != nil {
			log.Printf("orchestrator service: %v", err)
			exitCode.Add(1)
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(telemetrySignal)
		<-sig.Done()
		if err := srv.Close(ctx); err != nil {
			log.Printf("grpc service: %v", err)
			exitCode.Add(1)
		}
	}()

	wg.Wait()

	os.Exit(int(exitCode.Load()))
}
