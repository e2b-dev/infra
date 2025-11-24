package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"

	"go.uber.org/zap"
)

func main() {
	buildId := flag.String("build", "", "build id (only used when empty flag is false)")
	buildDir := flag.String("build-dir", "", "build directory")
	snapshot := flag.String("snapshot", "", "snapshot build id")
	snapshotDir := flag.String("snapshot-dir", "", "snapshot directory")
	firecrackerPath := flag.String("firecracker", "", "firecracker path")
	kernelPath := flag.String("kernel", "", "kernel path")
	cmd := flag.String("cmd", "", "command to run in the sandbox, after which the sandbox will be terminated (or snapshotted if a snapshot build id is provided). If not provided, the sandbox will run indefinitely, until cancelled.")
	logging := flag.Bool("log", false, "enable logging (it is pretty spammy)")

	flag.Parse()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt)

	// Logger is very spammy, because Populate on device pool periodically logs errors if the number of acquirable devices is less than the number of requested devices.
	if *logging {
		logger, err := zap.NewDevelopment()
		if err != nil {
			panic(fmt.Errorf("failed to create logger: %w", err))
		}
		zap.ReplaceGlobals(logger)
	}

	go func() {
		<-done

		cancel()
	}()
}
