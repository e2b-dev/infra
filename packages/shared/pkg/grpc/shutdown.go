package grpc

import (
	"time"

	"google.golang.org/grpc"
)

// GracefulStopWithTimeout invokes srv.GracefulStop and falls back to Stop() if
// it does not return within d. Returns true if graceful stop completed before
// the deadline.
//
// grpc.Server.GracefulStop blocks until all pending RPCs finish, with no
// built-in deadline. A stuck stream would otherwise block process shutdown
// past Nomad's kill_timeout and result in SIGKILL.
//
// Stop() force-closes transports
func GracefulStopWithTimeout(srv *grpc.Server, d time.Duration) bool {
	done := make(chan struct{})

	go func() {
		srv.GracefulStop()
		close(done)
	}()

	select {
	case <-done:
		return true
	case <-time.After(d):
		srv.Stop()

		return false
	}
}
