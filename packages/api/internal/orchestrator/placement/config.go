package placement

import "time"

const (
	maxRetries                  = 3
	maxExhaustedRetries         = 100
	maxStartingInstancesPerNode = 3

	// exhaustedRetryBackoff is the wait between retries of the exhausted node
	// pool, giving capacity time to free up without hot-spinning.
	exhaustedRetryBackoff = 100 * time.Millisecond
)
