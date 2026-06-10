package placement

import "time"

const (
	maxRetries                  = 3
	maxStartingInstancesPerNode = 3

	// When every node reports ResourceExhausted, PlaceSandbox keeps retrying
	// until the request deadline (capacity is usually transient), but waits a
	// jittered backoff after each full pass over the nodes so a saturated fleet
	// can't make it spin in a tight loop. The backoff grows exponentially from
	// base to max.
	resourceExhaustedBackoffBase    = 250 * time.Millisecond
	resourceExhaustedBackoffMaxWait = 5 * time.Second
)
