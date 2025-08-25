package utils

import (
	"fmt"
	"time"

	clickhouse "github.com/e2b-dev/infra/packages/clickhouse/pkg"
)

func ValidateDates(start time.Time, end time.Time) (time.Time, time.Time, error) {
	if start.After(clickhouse.MaxDate64) {
		return start, end, fmt.Errorf("start time cannot be after %s", clickhouse.MaxDate64)
	}

	if end.After(clickhouse.MaxDate64) {
		return start, end, fmt.Errorf("end time cannot be after %s", clickhouse.MaxDate64)
	}

	// Validate time range parameters
	if start.After(end) {
		return start, end, fmt.Errorf("start time cannot be after end time")
	}

	return start, end, nil
}
