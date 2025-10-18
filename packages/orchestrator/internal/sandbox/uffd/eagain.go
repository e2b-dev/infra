package uffd

import (
	"time"

	"go.uber.org/zap"
)

type eagainCounter struct {
	count     uint64
	startTime time.Time
	endTime   time.Time
	logger    *zap.Logger
	msg       string
}

func newEagainCounter(logger *zap.Logger, msg string) *eagainCounter {
	return &eagainCounter{
		count:     0,
		startTime: time.Time{},
		endTime:   time.Time{},
		logger:    logger,
		msg:       msg,
	}
}

func (c *eagainCounter) Increase() {
	if c.count == 0 {
		c.startTime = time.Now()
	}

	c.count++

	c.endTime = time.Now()
}

func (c *eagainCounter) log(closing bool) {
	if c.count > 0 {
		c.logger.Debug(
			c.msg,
			zap.Uint64("count", c.count),
			zap.Time("start", c.startTime),
			zap.Time("end", c.endTime),
			zap.Bool("closing", closing),
		)

		c.count = 0
	}
}

func (c *eagainCounter) Close() {
	c.log(true)
}

func (c *eagainCounter) Log() {
	c.log(false)
}
