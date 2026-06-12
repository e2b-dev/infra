//go:build !linux

package memory

// CollapseSelf is a no-op off Linux (MADV_COLLAPSE is Linux-only); envd runs
// only inside Linux microVMs.
func CollapseSelf() (Stats, error) {
	return Stats{}, nil
}
