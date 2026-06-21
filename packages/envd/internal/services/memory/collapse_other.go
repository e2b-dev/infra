//go:build !linux

package memory

import "context"

// CollapseSelf is a no-op off Linux (MADV_COLLAPSE is Linux-only); envd runs
// only inside Linux microVMs.
func CollapseSelf(_ context.Context) (Stats, error) {
	return Stats{}, nil
}
