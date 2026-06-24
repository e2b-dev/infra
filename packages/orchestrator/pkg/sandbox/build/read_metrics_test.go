//go:build linux

package build

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	blockmetrics "github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block/metrics"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

// swapReadFanoutMetrics points the package-level fan-out histograms at a
// manual reader for the duration of the test. NOT parallel-safe.
func swapReadFanoutMetrics(t *testing.T) *sdkmetric.ManualReader {
	t.Helper()

	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	m := mp.Meter("github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/build")

	prevSegments, prevBuilds := readSegmentsMetric, readBuildsMetric
	readSegmentsMetric = utils.Must(m.Int64Histogram("orchestrator.build.read.segments"))
	readBuildsMetric = utils.Must(m.Int64Histogram("orchestrator.build.read.builds"))
	t.Cleanup(func() { readSegmentsMetric, readBuildsMetric = prevSegments, prevBuilds })

	return reader
}

// fanoutSums returns, keyed by file_type label, the (sum, count) of the named
// histogram's datapoints.
func fanoutSums(t *testing.T, reader *sdkmetric.ManualReader, name string) map[string][2]int64 {
	t.Helper()

	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))

	out := map[string][2]int64{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			hist, ok := m.Data.(metricdata.Histogram[int64])
			if m.Name != name || !ok {
				continue
			}
			for _, dp := range hist.DataPoints {
				ft, ok := dp.Attributes.Value("file_type")
				require.True(t, ok, "datapoint missing file_type attribute")
				cur := out[ft.AsString()]
				out[ft.AsString()] = [2]int64{cur[0] + dp.Sum, cur[1] + int64(dp.Count)}
			}
		}
	}

	return out
}

// recordReadFanout emits one datapoint per histogram, tagged with the file
// type, for both the precomputed and the fallback attribute paths.
//
//nolint:paralleltest // swaps the package-level fan-out histograms
func TestRecordReadFanout(t *testing.T) {
	reader := swapReadFanoutMetrics(t)
	ctx := context.Background()

	recordReadFanout(ctx, Memfile, 13, 4)
	recordReadFanout(ctx, Memfile, 1, 1)
	recordReadFanout(ctx, Rootfs, 2, 2)
	recordReadFanout(ctx, DiffType("other"), 5, 5) // fallback attr path

	segments := fanoutSums(t, reader, "orchestrator.build.read.segments")
	assert.Equal(t, [2]int64{14, 2}, segments[string(Memfile)], "memfile segments sum/count")
	assert.Equal(t, [2]int64{2, 1}, segments[string(Rootfs)], "rootfs segments sum/count")
	assert.Equal(t, [2]int64{5, 1}, segments["other"], "fallback segments sum/count")

	builds := fanoutSums(t, reader, "orchestrator.build.read.builds")
	assert.Equal(t, [2]int64{5, 2}, builds[string(Memfile)], "memfile builds sum/count")
	assert.Equal(t, [2]int64{2, 1}, builds[string(Rootfs)], "rootfs builds sum/count")
}

// A ReadAt resolved entirely from uuid.Nil (zero-fill) mappings records one
// datapoint with zero segments and zero builds — fan-out is only counted for
// build-backed runs.
//
//nolint:paralleltest // swaps the package-level fan-out histograms
func TestFileReadAt_RecordsZeroFanoutForNilMapping(t *testing.T) {
	reader := swapReadFanoutMetrics(t)

	store, err := NewDiffStore(
		mustParseCfg(t),
		flagsWithMaxBuildCachePercentage(t, 90),
		t.TempDir(),
		time.Hour,
		time.Minute,
	)
	require.NoError(t, err)

	const size = 4096
	hdr, err := header.NewHeader(
		header.NewTemplateMetadata(uuid.Nil, size, size),
		[]header.BuildMap{{Offset: 0, Length: size, BuildId: uuid.Nil}},
	)
	require.NoError(t, err)

	m, err := blockmetrics.NewMetrics(noop.NewMeterProvider())
	require.NoError(t, err)
	f := NewFile(hdr, store, Memfile, nil, m)

	buf := make([]byte, size)
	n, err := f.ReadAt(t.Context(), buf, 0)
	require.NoError(t, err)
	require.Equal(t, size, n)

	segments := fanoutSums(t, reader, "orchestrator.build.read.segments")
	assert.Equal(t, [2]int64{0, 1}, segments[string(Memfile)], "zero-fill read records 0 segments")

	builds := fanoutSums(t, reader, "orchestrator.build.read.builds")
	assert.Equal(t, [2]int64{0, 1}, builds[string(Memfile)], "zero-fill read records 0 builds")
}
