package sandbox

import (
	"context"
	"os"
	"os/signal"
)

func listenProcessSignals(
	ctx context.Context, pid int, sigsToListen []os.Signal, callback func(sig os.Signal),
) {
	// Create a channel to receive signals
	sigChan := make(chan os.Signal, 1)

	// Specify the signals we want to listen to
	signal.Notify(sigChan, sigsToListen...)

	// Continuously listen for signals
	for {
		sig := <-sigChan
		callback(sig)
	}
}
