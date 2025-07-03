package port

import (
	"time"

	"github.com/rs/zerolog"
	net "github.com/shirou/gopsutil/v4/net"

	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
)

type Scanner struct {
	Processes chan net.ConnectionStat
	scanExit  chan struct{}
	subs      *smap.Map[*ScannerSubscriber]
	period    time.Duration
}

func (s *Scanner) Destroy() {
	close(s.scanExit)
}

func NewScanner(period time.Duration) *Scanner {
	return &Scanner{
		period:    period,
		subs:      smap.New[*ScannerSubscriber](),
		scanExit:  make(chan struct{}),
		Processes: make(chan net.ConnectionStat),
	}
}

func (s *Scanner) AddSubscriber(logger *zerolog.Logger, id string, filter *ScannerFilter) *ScannerSubscriber {
	subscriber := NewScannerSubscriber(logger, id, filter)
	s.subs.Insert(id, subscriber)

	return subscriber
}

func (s *Scanner) Unsubscribe(sub *ScannerSubscriber) {
	s.subs.Remove(sub.ID())
	sub.Destroy()
}

// ScanAndBroadcast starts scanning open TCP ports and broadcasts every open port to all subscribers.
func (s *Scanner) ScanAndBroadcast() {
	for {
		// tcp monitors both ipv4 and ipv6 connections.
		processes, _ := net.Connections("tcp")
		for _, sub := range s.subs.Items() {
			sub.Signal(processes)
		}
		select {
		case <-s.scanExit:
			return
		default:
			time.Sleep(s.period)
		}
	}
}
