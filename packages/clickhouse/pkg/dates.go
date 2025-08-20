package clickhouse

import "time"

// MaxDate64 representable in Clickhouse https://clickhouse.com/docs/sql-reference/data-types/datetime64
var MaxDate64 = time.Date(2299, 12, 31, 23, 59, 59, 999999999, time.UTC)
