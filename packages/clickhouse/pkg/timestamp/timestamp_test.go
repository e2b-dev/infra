package timestamp

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestUnixNano(t *testing.T) {
	t.Parallel()

	timestamp := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	assert.Equal(t, timestamp.UnixNano(), UnixNano(timestamp))

	location := time.FixedZone("test", 3600)
	assert.Equal(t, timestamp.UnixNano(), UnixNano(timestamp.In(location)))
}

func TestUnixNanoClampsToInt64Range(t *testing.T) {
	t.Parallel()

	max := time.Unix(maxUnixSeconds, 0).UTC().UnixNano()
	min := time.Unix(-maxUnixSeconds, 0).UTC().UnixNano()

	assert.Equal(t, max, UnixNano(time.Date(2299, 12, 31, 23, 59, 59, 0, time.UTC)))
	assert.Equal(t, min, UnixNano(time.Date(1600, 1, 1, 0, 0, 0, 0, time.UTC)))
}
