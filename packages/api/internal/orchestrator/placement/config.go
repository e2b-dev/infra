package placement

import "time"

const (
	maxRetries                  = 3
	maxStartingInstancesPerNode = 3

	// Bound placement retries when nodes report ResourceExhausted so a saturated
	// fleet can't make PlaceSandbox spin until the request deadline. The backoff
	// grows exponentially from base to max (with jitter) to avoid hammering the
	// fleet under sustained capacity pressure.
	maxResourceExhaustedRetries     = 8
	resourceExhaustedBackoffBase    = 250 * time.Millisecond
	resourceExhaustedBackoffMaxWait = 5 * time.Second
)
