//go:build !linux

package port

// reapAdoptedSocat is a no-op on non-Linux platforms; the port forwarder and its
// socat children are Linux-only.
func reapAdoptedSocat(_ int) {}
