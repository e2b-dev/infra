package fc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

const (
	// metricsReaderBufSize is the scanner buffer for a single Firecracker metrics line.
	// 1 MB is well above the size of any single Firecracker metrics JSON line.
	metricsReaderBufSize = 1 * 1024 * 1024 // 1 MB

	// metricsFlushInterval controls how often we trigger a Firecracker metrics flush.
	// Matches the host stats sampling interval (HostStatsSamplingInterval, default 5 s).
	metricsFlushInterval = 5 * time.Second
)

var (
	fcMeter = otel.GetMeterProvider().Meter("orchestrator.internal.sandbox.fc")

	// direction attributes reused on every record call.
	directionKey = attribute.Key("direction")
	attrTX       = metric.WithAttributes(directionKey.String("tx"))
	attrRX       = metric.WithAttributes(directionKey.String("rx"))

	// Counters — global totals, no sandbox_id to avoid high cardinality.
	fcNetFails         = utils.Must(telemetry.GetCounter(fcMeter, telemetry.SandboxFCNetFails))
	fcNetNoAvailBuffer = utils.Must(telemetry.GetCounter(fcMeter, telemetry.SandboxFCNetNoAvailBuffer))
	fcNetTapIOFails    = utils.Must(telemetry.GetCounter(fcMeter, telemetry.SandboxFCNetTapIOFails))

	// Histograms — per-sandbox distribution per flush, no sandbox_id.
	fcNetBytes                = utils.Must(telemetry.GetHistogram(fcMeter, telemetry.SandboxFCNetBytes))
	fcNetPackets              = utils.Must(telemetry.GetHistogram(fcMeter, telemetry.SandboxFCNetPackets))
	fcNetCount                = utils.Must(telemetry.GetHistogram(fcMeter, telemetry.SandboxFCNetCount))
	fcNetRateLimiterThrottled = utils.Must(telemetry.GetHistogram(fcMeter, telemetry.SandboxFCNetRateLimiterThrottled))
	// TX-only: no RX equivalent in Firecracker metrics.
	fcNetRateLimiterEventCount = utils.Must(telemetry.GetHistogram(fcMeter, telemetry.SandboxFCNetRateLimiterEventCount))
	fcNetRemainingReqs         = utils.Must(telemetry.GetHistogram(fcMeter, telemetry.SandboxFCNetRemainingReqs))
)

// firecrackerNetMetrics holds the Firecracker net metrics fields we care about.
// Firecracker serializes SharedIncMetric fields as per-flush deltas (not cumulative totals):
// each JSON line contains the increment since the previous flush.
// Flush interval defaults to 60 s; additional flushes are triggered by FlushMetrics API calls.
type firecrackerNetMetrics struct {
	// TX
	TxBytesCount            uint64 `json:"tx_bytes_count"`
	TxPacketsCount          uint64 `json:"tx_packets_count"`
	TxCount                 uint64 `json:"tx_count"`
	TxFails                 uint64 `json:"tx_fails"`
	TxRateLimiterThrottled  uint64 `json:"tx_rate_limiter_throttled"`
	TxRateLimiterEventCount uint64 `json:"tx_rate_limiter_event_count"`
	TxRemainingReqsCount    uint64 `json:"tx_remaining_reqs_count"`
	NoTxAvailBuffer         uint64 `json:"no_tx_avail_buffer"`
	TapWriteFails           uint64 `json:"tap_write_fails"`
	// RX
	RxBytesCount           uint64 `json:"rx_bytes_count"`
	RxPacketsCount         uint64 `json:"rx_packets_count"`
	RxCount                uint64 `json:"rx_count"`
	RxFails                uint64 `json:"rx_fails"`
	RxRateLimiterThrottled uint64 `json:"rx_rate_limiter_throttled"`
	NoRxAvailBuffer        uint64 `json:"no_rx_avail_buffer"`
	TapReadFails           uint64 `json:"tap_read_fails"`
}

// firecrackerMetrics is the top-level structure of one Firecracker metrics JSON line.
type firecrackerMetrics struct {
	Net firecrackerNetMetrics `json:"net"`
}

// startMetricsReader opens the metrics FIFO and starts a goroutine that reads
// Firecracker metrics lines and exports net device metrics via OTEL.
// It must be called before setMetrics so that the FIFO is open for reading
// before Firecracker opens the write end in response to PUT /metrics.
func (p *Process) startMetricsReader(ctx context.Context) {
	// Detach from the request context so the goroutine runs for the VM's lifetime
	// but still inherits trace values for logging.
	ctx = context.WithoutCancel(ctx)
	sandboxID := p.files.SandboxID
	metricsPath := p.metricsPath

	// Flusher: periodically triggers a Firecracker metrics flush so the reader receives
	// fresh data at metricsFlushInterval instead of the default 60 s.
	go func() {
		ticker := time.NewTicker(metricsFlushInterval)
		defer ticker.Stop()

		for {
			select {
			case <-p.Exit.Done():
				return
			case <-ticker.C:
				if err := p.client.flushMetrics(ctx); err != nil {
					logger.L().Warn(ctx, "failed to flush fc metrics",
						zap.Error(err),
						logger.WithSandboxID(sandboxID),
					)
				}
			}
		}
	}()

	go func() {
		// O_RDWR opens without blocking (no need to wait for a writer).
		// We keep this FD solely to unblock the open; the scanner reads from
		// a separate O_RDONLY FD below. On process exit we close the O_RDWR FD
		// to drop our write reference — once Firecracker also exits, the
		// O_RDONLY read receives EOF and the goroutine exits cleanly.
		rwFd, err := os.OpenFile(metricsPath, os.O_RDWR, os.ModeNamedPipe)
		if err != nil {
			logger.L().Warn(ctx, "failed to open fc metrics FIFO",
				zap.Error(err),
				logger.WithSandboxID(sandboxID),
			)

			return
		}

		// O_RDONLY succeeds immediately because O_RDWR already established both ends.
		rFd, err := os.OpenFile(metricsPath, os.O_RDONLY, os.ModeNamedPipe)
		if err != nil {
			rwFd.Close()
			logger.L().Warn(ctx, "failed to open fc metrics FIFO for reading",
				zap.Error(err),
				logger.WithSandboxID(sandboxID),
			)

			return
		}
		defer rFd.Close()

		// Drop our write reference on exit so the scanner can receive EOF.
		go func() {
			<-p.Exit.Done()
			rwFd.Close()
		}()

		scanner := bufio.NewScanner(rFd)
		scanner.Buffer(make([]byte, metricsReaderBufSize), metricsReaderBufSize)

		for scanner.Scan() {
			var m firecrackerMetrics
			if err := json.Unmarshal(scanner.Bytes(), &m); err != nil {
				logger.L().Warn(ctx, "failed to parse fc metrics line",
					zap.Error(err),
					logger.WithSandboxID(sandboxID),
				)

				continue
			}

			n := &m.Net

			// TX histograms — values are already per-flush deltas from Firecracker.
			fcNetBytes.Record(ctx, int64(n.TxBytesCount), attrTX)
			fcNetPackets.Record(ctx, int64(n.TxPacketsCount), attrTX)
			fcNetCount.Record(ctx, int64(n.TxCount), attrTX)
			fcNetRateLimiterEventCount.Record(ctx, int64(n.TxRateLimiterEventCount), attrTX)
			fcNetRemainingReqs.Record(ctx, int64(n.TxRemainingReqsCount), attrTX)

			// Only record throttled when non-zero to avoid polluting the histogram with idle intervals.
			if n.TxRateLimiterThrottled > 0 {
				fcNetRateLimiterThrottled.Record(ctx, int64(n.TxRateLimiterThrottled), attrTX)
			}

			// RX histograms.
			fcNetBytes.Record(ctx, int64(n.RxBytesCount), attrRX)
			fcNetPackets.Record(ctx, int64(n.RxPacketsCount), attrRX)
			fcNetCount.Record(ctx, int64(n.RxCount), attrRX)

			if n.RxRateLimiterThrottled > 0 {
				fcNetRateLimiterThrottled.Record(ctx, int64(n.RxRateLimiterThrottled), attrRX)
			}

			// Global error/event counters (only increment on non-zero values).
			if n.TxFails > 0 {
				fcNetFails.Add(ctx, int64(n.TxFails), attrTX)
			}
			if n.RxFails > 0 {
				fcNetFails.Add(ctx, int64(n.RxFails), attrRX)
			}
			if n.NoTxAvailBuffer > 0 {
				fcNetNoAvailBuffer.Add(ctx, int64(n.NoTxAvailBuffer), attrTX)
			}
			if n.NoRxAvailBuffer > 0 {
				fcNetNoAvailBuffer.Add(ctx, int64(n.NoRxAvailBuffer), attrRX)
			}
			if n.TapWriteFails > 0 {
				fcNetTapIOFails.Add(ctx, int64(n.TapWriteFails), attrTX)
			}
			if n.TapReadFails > 0 {
				fcNetTapIOFails.Add(ctx, int64(n.TapReadFails), attrRX)
			}
		}

		if err := scanner.Err(); err != nil {
			if errors.Is(err, bufio.ErrTooLong) {
				logger.L().Error(ctx, "fc metrics line exceeded buffer size, metrics reader stopped",
					zap.Int("bufferSizeBytes", metricsReaderBufSize),
					logger.WithSandboxID(sandboxID),
				)
			} else {
				logger.L().Warn(ctx, "fc metrics FIFO scanner error",
					zap.Error(err),
					logger.WithSandboxID(sandboxID),
				)
			}
		}
	}()
}
