//go:build !linux

package fsfreeze

// New returns a no-op Freezer on non-Linux platforms; filesystem freezing is a
// Linux-only kernel feature.
func New() Freezer {
	return noopFreezer{}
}

type noopFreezer struct{}

func (noopFreezer) Freeze(string) error { return nil }

func (noopFreezer) Thaw(string) error { return nil }
