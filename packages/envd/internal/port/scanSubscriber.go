package port

import (
	"github.com/rs/zerolog"
	net "github.com/shirou/gopsutil/v4/net"
)

// If we want to create a listener/subscriber pattern somewhere else we should move
// from a concrete implementation to combination of generics and interfaces.

type ScannerSubscriber struct {
	logger   *zerolog.Logger
	filter   *ScannerFilter
	Messages chan ([]net.ConnectionStat)
	id       string
}

func NewScannerSubscriber(logger *zerolog.Logger, id string, filter *ScannerFilter) *ScannerSubscriber {
	return &ScannerSubscriber{
		logger:   logger,
		id:       id,
		filter:   filter,
		Messages: make(chan []net.ConnectionStat),
	}
}

func (ss *ScannerSubscriber) ID() string {
	return ss.id
}

func (ss *ScannerSubscriber) Destroy() {
	close(ss.Messages)
}

func (ss *ScannerSubscriber) Signal(proc []net.ConnectionStat) {
	// Filter isn't specified. Accept everything.
	if ss.filter == nil {
		ss.Messages <- proc
	} else {
		filtered := []net.ConnectionStat{}
		for i := range proc {
			// We need to access the list directly otherwise there will be implicit memory aliasing
			// If the filter matched a process, we will send it to a channel.
			if ss.filter.Match(&proc[i]) {
				filtered = append(filtered, proc[i])
			}
		}
		ss.Messages <- filtered
	}
}
