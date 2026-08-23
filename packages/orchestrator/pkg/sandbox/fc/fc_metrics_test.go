//go:build linux

package fc

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// One metrics line as Firecracker writes it, trimmed to the block section.
// It carries the fields we do not export (read_agg, discard_count, ...) and the
// per-device "block_rootfs" key alongside the aggregate, so the fixture fails if
// a json tag drifts or the aggregate stops being the key we read.
const fcBlockMetricsLine = `{
  "block": {
    "activate_fails": 0,
    "cfg_fails": 0,
    "no_avail_buffer": 3,
    "event_fails": 1,
    "execute_fails": 2,
    "invalid_reqs_count": 0,
    "flush_count": 41,
    "queue_event_count": 8321,
    "rate_limiter_event_count": 12,
    "update_count": 0,
    "update_fails": 0,
    "read_bytes": 4194304,
    "write_bytes": 8388608,
    "read_count": 512,
    "write_count": 1024,
    "read_agg": {"min_us": 40, "max_us": 9100, "sum_us": 730000},
    "write_agg": {"min_us": 55, "max_us": 12000, "sum_us": 990000},
    "rate_limiter_throttled_events": 7,
    "io_engine_throttled_events": 0,
    "remaining_reqs_count": 96,
    "discard_count": 5,
    "write_zeroes_count": 6
  },
  "block_rootfs": {
    "queue_event_count": 4096
  }
}`

func TestFirecrackerBlockMetricsDecode(t *testing.T) {
	t.Parallel()

	var m firecrackerMetrics
	require.NoError(t, json.Unmarshal([]byte(fcBlockMetricsLine), &m))

	assert.Equal(t, firecrackerBlockMetrics{
		ReadBytes:                  4194304,
		WriteBytes:                 8388608,
		ReadCount:                  512,
		WriteCount:                 1024,
		QueueEventCount:            8321,
		RateLimiterThrottledEvents: 7,
		RateLimiterEventCount:      12,
		IOEngineThrottledEvents:    0,
		NoAvailBuffer:              3,
		ExecuteFails:               2,
		EventFails:                 1,
		RemainingReqsCount:         96,
	}, m.Block)
}

// A queue that receives no notifications is the signal a stalled block device
// produces, so zero must survive decoding as a value rather than being lost.
func TestFirecrackerBlockMetricsDecodeZeroQueueEvents(t *testing.T) {
	t.Parallel()

	var m firecrackerMetrics
	require.NoError(t, json.Unmarshal([]byte(`{"block":{"queue_event_count":0,"remaining_reqs_count":85}}`), &m))

	assert.Zero(t, m.Block.QueueEventCount)
	assert.Equal(t, uint64(85), m.Block.RemainingReqsCount)
}
