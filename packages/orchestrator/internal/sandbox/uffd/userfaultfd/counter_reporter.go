package userfaultfd

import (
	"context"
	"time"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

// counterReporter tracks counts per type/key and logs them all in a single message.
type counterReporter struct {
	counts    map[string]uint64
	startTime time.Time
	endTime   time.Time
	logger    logger.Logger
	msg       string
}

func newCounterReporter(logger logger.Logger, msg string) *counterReporter {
	return &counterReporter{
		counts:    make(map[string]uint64),
		startTime: time.Time{},
		endTime:   time.Time{},
		logger:    logger,
		msg:       msg,
	}
}

func (c *counterReporter) Increase(eventType string) {
	if c.totalCount() == 0 {
		c.startTime = time.Now()
	}

	c.counts[eventType]++

	c.endTime = time.Now()
}

func (c *counterReporter) totalCount() uint64 {
	var total uint64
	for _, count := range c.counts {
		total += count
	}

	return total
}

func (c *counterReporter) log(ctx context.Context, closing bool) {
	total := c.totalCount()
	if total > 0 {
		fields := []zap.Field{
			zap.Uint64("count", total),
			zap.Time("start", c.startTime),
			zap.Time("end", c.endTime),
			zap.Bool("closing", closing),
		}

		for eventType, count := range c.counts {
			fields = append(fields, zap.Uint64(eventType, count))
		}

		c.logger.Debug(ctx, c.msg, fields...)

		c.counts = make(map[string]uint64)
	}
}

func (c *counterReporter) Close(ctx context.Context) {
	c.log(ctx, true)
}

func (c *counterReporter) Log(ctx context.Context) {
	c.log(ctx, false)
}
