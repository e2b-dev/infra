package port

import (
	"github.com/shirou/gopsutil/v4/net"
)

// If we want to create a listener/subscriber pattern somewhere else we should move
// from a concrete implementation to combination of generics and interfaces.

type ScannerSubscriber struct {
	filter   *ScannerFilter
	Messages chan ([]net.ConnectionStat)
	id       string
}

func NewScannerSubscriber(id string, filter *ScannerFilter) *ScannerSubscriber {
	return &ScannerSubscriber{
		id:       id,
		filter:   filter,
		Messages: make(chan []net.ConnectionStat),
	}
}

func (ss *ScannerSubscriber) ID() string {
	return ss.id
}

// Signal delivers a scan result to the subscriber, blocking until the receiver
// takes it or exit is closed.
func (ss *ScannerSubscriber) Signal(proc []net.ConnectionStat, exit <-chan struct{}) {
	msg := proc
	if ss.filter != nil {
		filtered := make([]net.ConnectionStat, 0, len(proc))
		for i := range proc {
			// We need to access the list directly otherwise there will be implicit memory aliasing
			// If the filter matched a process, we will send it to a channel.
			if ss.filter.Match(&proc[i]) {
				filtered = append(filtered, proc[i])
			}
		}
		msg = filtered
	}

	select {
	case ss.Messages <- msg:
	// Messages is unbuffered, so this send waits for the receiver. Without the
	// exit case it would wait forever once the receiver stops consuming.
	case <-exit:
	}
}
