// Package timestamp converts Go timestamps for use in ClickHouse queries.
package timestamp

import "time"

// maxUnixSeconds is the largest Unix-second value whose nanosecond
// representation fits in a signed int64. time.Time.UnixNano is undefined
// beyond approximately 2262-04-11.
const maxUnixSeconds = (1<<63 - 1) / int64(time.Second)

// UnixNano returns t as whole-second nanoseconds since the Unix epoch, clamped
// to the int64 range accepted by ClickHouse's fromUnixTimestamp64Nano.
func UnixNano(t time.Time) int64 {
	seconds := t.UTC().Unix()
	switch {
	case seconds > maxUnixSeconds:
		seconds = maxUnixSeconds
	case seconds < -maxUnixSeconds:
		seconds = -maxUnixSeconds
	}

	return time.Unix(seconds, 0).UTC().UnixNano()
}
