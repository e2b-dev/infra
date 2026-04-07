package metrics

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/attribute"
)

type Recorder interface {
	ObserveHTTPRequestDuration(ctx context.Context, duration time.Duration, attributes []attribute.KeyValue)
}
