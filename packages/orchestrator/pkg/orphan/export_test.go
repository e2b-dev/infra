//go:build linux

// export_test.go is compiled only during testing and exposes private functions
// to the external test package. It is not included in production binaries.
package orphan

import "time"

// ---- scanner.go ----

var (
	ExtractAPISocket    = extractAPISocket
	FifoNameToSocketName = fifoNameToSocketName
	ScanOrphanedSockets = scanOrphanedSockets
	ScanOrphanedFIFOs   = scanOrphanedFIFOs
)

// ---- cleaner.go ----

var (
	IsNoSuchProcess  = isNoSuchProcess
	ParseIPTablesRule = parseIPTablesRule
	ContainsInterface = containsInterface
	CleanOrphanedSockets = cleanOrphanedSockets
	CleanOrphanedFIFOs   = cleanOrphanedFIFOs
)

// ---- reconciler.go ----

var NextSweepTime = nextSweepTime

// NewReconcilerConfig returns a Config with defaults applied (zero value).
func NewReconcilerConfig() Config {
	c := Config{}
	c.setDefaults()
	return c
}

// NewReconcilerConfigWith returns a Config with explicit TmpDirs and MinOrphanAge,
// then applies defaults for any remaining zero fields.
func NewReconcilerConfigWith(tmpDirs []string, minAge time.Duration) Config {
	c := Config{TmpDirs: tmpDirs, MinOrphanAge: minAge}
	c.setDefaults()
	return c
}
