package utils

import (
	"fmt"
	"time"

	clickhouse "github.com/e2b-dev/infra/packages/clickhouse/pkg"
)

func ValidateDates(paramStart *int64, paramEnd *int64, defaultStart time.Time, defaultEnd time.Time) (start time.Time, end time.Time, err error) {
	start = defaultStart
	end = defaultEnd

	if paramStart != nil {
		start = time.Unix(*paramStart, 0)
	}

	if start.After(clickhouse.MaxDate64) {
		return start, end, fmt.Errorf("start time cannot be after %s", clickhouse.MaxDate64)
	}

	if paramEnd != nil {
		end = time.Unix(*paramEnd, 0)
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
