package middleware

import (
	"net/http"
	"regexp"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// Based on https://github.com/gin-contrib/zap

// Fn is a function to get zap fields from gin.Context
type Fn func(c *gin.Context) []zapcore.Field

// Skipper is a function to skip logs based on provided Context
type Skipper func(c *gin.Context) bool

type Config struct {
	TimeFormat      string
	UTC             bool
	SkipPaths       []string
	SkipPathRegexps []*regexp.Regexp
	Context         Fn
	DefaultLevel    zapcore.Level
	// skip is a Skipper that indicates which logs should not be written.
	// Optional.
	Skipper Skipper
}

func LoggingMiddleware(logger *zap.Logger, conf Config) gin.HandlerFunc {
	skipPaths := make(map[string]bool, len(conf.SkipPaths))
	for _, path := range conf.SkipPaths {
		skipPaths[path] = true
	}

	return func(c *gin.Context) {
		start := time.Now()

		// Preserve this if any middleware modifies these values
		path := c.Request.URL.Path
		query := c.Request.URL.RawQuery
		c.Next()
		track := true

		if _, ok := skipPaths[path]; ok || (conf.Skipper != nil && conf.Skipper(c)) {
			track = false
		}

		if track && len(conf.SkipPathRegexps) > 0 {
			for _, reg := range conf.SkipPathRegexps {
				if !reg.MatchString(path) {
					continue
				}

				track = false
				break
			}
		}

		if track {
			end := time.Now()
			latency := end.Sub(start)
			if conf.UTC {
				end = end.UTC()
			}

			status := c.Writer.Status()

			fields := []zapcore.Field{
				zap.Int("status", status),
				zap.String("method", c.Request.Method),
				zap.String("path", path),
				zap.String("query", query),
				zap.String("ip", c.ClientIP()),
				zap.String("user-agent", c.Request.UserAgent()),
				zap.Duration("latency", latency),
			}
			if conf.TimeFormat != "" {
				fields = append(fields, zap.String("time", end.Format(conf.TimeFormat)))
			}

			if conf.Context != nil {
				fields = append(fields, conf.Context(c)...)
			}

			// Log errors if present
			if len(c.Errors) > 0 {
				for _, e := range c.Errors.Errors() {
					if status >= http.StatusInternalServerError {
						logger.Error(e, fields...)
					} else {
						logger.Warn(e, fields...)
					}
				}
			} else {
				// No errors, let's log the request
				level := conf.DefaultLevel
				if status >= http.StatusInternalServerError {
					level = zapcore.ErrorLevel
				} else if status >= http.StatusBadRequest {
					level = zapcore.WarnLevel
				}

				logger.Log(level, path, fields...)
			}
		}
	}
}
