package userfaultfd

import (
	"context"
	"time"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

type counterReporter struct {
	count     uint64
	startTime time.Time
	endTime   time.Time
	logger    logger.Logger
	msg       string
}

func newCounterReporter(logger logger.Logger, msg string) *counterReporter {
	return &counterReporter{
		count:     0,
		startTime: time.Time{},
		endTime:   time.Time{},
		logger:    logger,
		msg:       msg,
	}
}

func (c *counterReporter) Increase() {
	if c.count == 0 {
		c.startTime = time.Now()
	}

	c.count++

	c.endTime = time.Now()
}

func (c *counterReporter) log(ctx context.Context, closing bool) {
	if c.count > 0 {
		c.logger.Debug(ctx,
			c.msg,
			zap.Uint64("count", c.count),
			zap.Time("start", c.startTime),
			zap.Time("end", c.endTime),
			zap.Bool("closing", closing),
		)

		c.count = 0
	}
}

func (c *counterReporter) Close(ctx context.Context) {
	c.log(ctx, true)
}

func (c *counterReporter) Log(ctx context.Context) {
	c.log(ctx, false)
}
