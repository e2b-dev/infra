package uffd

import (
	"time"

	"go.uber.org/zap"
)

type EagainCounter struct {
	count     uint64
	startTime time.Time
	endTime   time.Time
	logger    *zap.Logger
	msg       string
}

func NewEagainCounter(logger *zap.Logger, msg string) *EagainCounter {
	return &EagainCounter{
		count:     0,
		startTime: time.Time{},
		endTime:   time.Time{},
		logger:    logger,
		msg:       msg,
	}
}

func (c *EagainCounter) Increase() {
	if c.count == 0 {
		c.startTime = time.Now()
	}

	c.count++

	c.endTime = time.Now()
}

func (c *EagainCounter) log(closing bool) {
	if c.count > 0 {
		c.logger.Debug(
			c.msg,
			zap.Uint64("count", c.count),
			zap.Time("start time", c.startTime),
			zap.Time("end time", c.endTime),
			zap.Bool("closing", closing),
		)

		c.count = 0
	}
}

func (c *EagainCounter) Close() {
	c.log(true)
}

func (c *EagainCounter) Log() {
	c.log(false)
}
