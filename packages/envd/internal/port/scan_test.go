package port

import (
	"bytes"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/shirou/gopsutil/v4/net"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewScanner(t *testing.T) {
	buf := &bytes.Buffer{}
	logger := zerolog.New(buf)

	s := NewScanner(&logger, 50*time.Millisecond)
	require.NotNil(t, s)
	assert.NotNil(t, s.Processes)
	assert.NotNil(t, s.subs)
	assert.Equal(t, 50*time.Millisecond, s.period)

	sub := s.AddSubscriber(&logger, "test-sub", &ScannerFilter{
		IPs:   []string{"127.0.0.1"},
		State: "LISTEN",
	})
	require.NotNil(t, sub)
	assert.Equal(t, "test-sub", sub.ID())

	s.Unsubscribe(sub)
	_, ok := <-sub.Messages
	assert.False(t, ok, "subscriber channel should be closed upon unsubscribe")
}

func TestScanAndBroadcastLifecycle(t *testing.T) {
	buf := &bytes.Buffer{}
	logger := zerolog.New(buf)

	s := NewScanner(&logger, 10*time.Millisecond)
	sub := s.AddSubscriber(&logger, "sub-1", nil)

	received := make(chan struct{}, 1)
	drainDone := make(chan struct{})
	go func() {
		defer close(drainDone)
		for range sub.Messages {
			select {
			case received <- struct{}{}:
			default:
			}
		}
	}()

	done := make(chan struct{})
	go func() {
		s.ScanAndBroadcast()
		close(done)
	}()

	select {
	case <-received:
		// Received at least one scan message
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for scan message")
	}

	s.Unsubscribe(sub)
	s.Destroy()

	select {
	case <-done:
		// Scanner goroutine exited promptly
	case <-time.After(1 * time.Second):
		t.Fatal("ScanAndBroadcast did not exit after Destroy()")
	}

	<-drainDone
}

func TestSubscriberFilter(t *testing.T) {
	buf := &bytes.Buffer{}
	logger := zerolog.New(buf)

	sub := NewScannerSubscriber(&logger, "sub-filter", &ScannerFilter{
		IPs:   []string{"127.0.0.1", "localhost"},
		State: "LISTEN",
	})
	defer sub.Destroy()

	conns := []net.ConnectionStat{
		{
			Status: "LISTEN",
			Laddr:  net.Addr{IP: "127.0.0.1", Port: 8080},
		},
		{
			Status: "ESTABLISHED",
			Laddr:  net.Addr{IP: "127.0.0.1", Port: 8080},
		},
		{
			Status: "LISTEN",
			Laddr:  net.Addr{IP: "192.168.1.1", Port: 8080},
		},
	}

	go sub.Signal(conns)

	select {
	case filtered := <-sub.Messages:
		require.Len(t, filtered, 1)
		assert.Equal(t, uint32(8080), filtered[0].Laddr.Port)
		assert.Equal(t, "127.0.0.1", filtered[0].Laddr.IP)
		assert.Equal(t, "LISTEN", filtered[0].Status)
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for filtered messages")
	}
}
