package port

import (
	"github.com/rs/zerolog"
	"github.com/shirou/gopsutil/v4/net"
)

// If we want to create a listener/subscriber pattern somewhere else we should move
// from a concrete implementation to combination of generics and interfaces.

type ScannerSubscriber struct {
	logger   *zerolog.Logger
	filter   *ScannerFilter
	Messages chan ([]net.ConnectionStat)
	done     chan struct{}
	id       string
}

func NewScannerSubscriber(logger *zerolog.Logger, id string, filter *ScannerFilter) *ScannerSubscriber {
	return &ScannerSubscriber{
		logger:   logger,
		id:       id,
		filter:   filter,
		Messages: make(chan []net.ConnectionStat),
		done:     make(chan struct{}),
	}
}

func (ss *ScannerSubscriber) ID() string {
	return ss.id
}

// Destroy signals the subscriber to stop. Any Signal() call blocked waiting
// for a receiver will return immediately. The Messages channel is left open so
// in-progress receivers can drain it; callers exit via ctx.Done() instead.
func (ss *ScannerSubscriber) Destroy() {
	close(ss.done)
}

// Signal delivers a port scan result to the subscriber. If the receiver is not
// ready (e.g. it has stopped consuming after context cancellation), Signal
// returns immediately rather than blocking, so ScanAndBroadcast is never
// stalled by a single slow or stopped subscriber.
func (ss *ScannerSubscriber) Signal(proc []net.ConnectionStat) {
	msg := proc
	if ss.filter != nil {
		filtered := make([]net.ConnectionStat, 0, len(proc))
		for i := range proc {
			// Access the slice directly to avoid implicit memory aliasing.
			if ss.filter.Match(&proc[i]) {
				filtered = append(filtered, proc[i])
			}
		}
		msg = filtered
	}

	select {
	case ss.Messages <- msg:
	case <-ss.done:
	}
}
