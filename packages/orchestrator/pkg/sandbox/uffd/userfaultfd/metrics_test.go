//go:build linux

package userfaultfd

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

// TestServeMetric exercises the serve-stats recording exactly as the Serve
// worker does — serveTimer.Begin() + RecordRaw(ctx, bytes, serveAttrs[class][result])
// — and asserts the resulting orchestrator.sandbox.uffd.serve datapoints carry
// the right page_class / result attributes, fault counts and faulted bytes.
//
// The real serve loop runs in a forked child process (TestHelperServingProcess),
// so its metrics can't be collected from the parent; this test pins the metric
// definition and the page_class/result attribute table in-process instead. It
// is intentionally NOT parallel: it swaps the package-level serveTimer.
//
//nolint:paralleltest // swaps the package-level serveTimer
func TestServeMetric(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))

	prev := serveTimer
	serveTimer = utils.Must(telemetry.NewTimerFactory(
		mp.Meter("github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/uffd/userfaultfd"),
		serveMetricName,
		"Time to serve a UFFD page fault",
		"Bytes faulted into the guest",
		"UFFD faults served",
	))
	t.Cleanup(func() { serveTimer = prev })

	ctx := context.Background()
	pageSize := int64(header.PageSize)

	// Record a mix of faults the way the worker would.
	record := func(class pageClass, result faultResult, bytes int64, n int) {
		for range n {
			sw := serveTimer.Begin()
			sw.RecordRaw(ctx, bytes, serveAttrs[class][result])
		}
	}
	record(pageClassNew, faultResultInstalled, pageSize, 3)
	record(pageClassZero, faultResultInstalled, pageSize, 1)
	record(pageClassResident, faultResultPresent, 0, 2)
	record(pageClassNew, faultResultPresent, 0, 1)
	record(pageClassNew, faultResultDeferred, 0, 1)

	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(ctx, &rm))

	counts, bytesSum := collectServe(t, rm)

	// new+installed: 3 faults, each one page of faulted bytes.
	assert.Equal(t, int64(3), counts[key("new", "installed")], "new+installed fault count")
	assert.Equal(t, 3*pageSize, bytesSum[key("new", "installed")], "new+installed faulted bytes")

	// zero+installed: 1 fault, one page installed (zeros) → counted as faulted.
	assert.Equal(t, int64(1), counts[key("zero", "installed")])
	assert.Equal(t, pageSize, bytesSum[key("zero", "installed")])

	// resident short-circuit: counted as "present", nothing copied.
	assert.Equal(t, int64(2), counts[key("resident", "present")])
	assert.Equal(t, int64(0), bytesSum[key("resident", "present")])

	// lost install race (EEXIST): counted as "present", no bytes attributed
	// to this serve — keeps sum(bytes) == pages actually copied by serves.
	assert.Equal(t, int64(1), counts[key("new", "present")])
	assert.Equal(t, int64(0), bytesSum[key("new", "present")])

	// deferred: counted, no bytes faulted.
	assert.Equal(t, int64(1), counts[key("new", "deferred")])
	assert.Equal(t, int64(0), bytesSum[key("new", "deferred")])
}

func key(pageClass, result string) string { return pageClass + "/" + result }

func attrKey(t *testing.T, attrs attribute.Set) string {
	t.Helper()
	pc, ok := attrs.Value("page_class")
	require.True(t, ok, "datapoint missing page_class attribute")
	r, ok := attrs.Value("result")
	require.True(t, ok, "datapoint missing result attribute")

	return key(pc.AsString(), r.AsString())
}

// collectServe returns, keyed by "page_class/result", the fault count (from the
// duration histogram) and the faulted-bytes sum (the "By"-unit counter) for the
// serve metric.
func collectServe(t *testing.T, rm metricdata.ResourceMetrics) (counts map[string]int64, bytesSum map[string]int64) {
	t.Helper()

	counts = map[string]int64{}
	bytesSum = map[string]int64{}
	sawMetric := false

	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != serveMetricName {
				continue
			}
			sawMetric = true

			switch data := m.Data.(type) {
			case metricdata.Histogram[int64]:
				for _, dp := range data.DataPoints {
					counts[attrKey(t, dp.Attributes)] += int64(dp.Count)
				}
			case metricdata.Sum[int64]:
				// The TimerFactory emits two same-named counters; the
				// faulted-bytes one carries the "By" unit.
				if m.Unit != "By" {
					continue
				}
				for _, dp := range data.DataPoints {
					bytesSum[attrKey(t, dp.Attributes)] += dp.Value
				}
			}
		}
	}

	require.True(t, sawMetric, "metric %q not found in collected metrics", serveMetricName)

	return counts, bytesSum
}
