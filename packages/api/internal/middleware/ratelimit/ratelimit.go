package ratelimit

import (
	"context"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis_rate/v10"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/auth/pkg/auth"
	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	redis_utils "github.com/e2b-dev/infra/packages/shared/pkg/redis"
)

const rateLimitPefix = "ratelimit"

// Config defines the rate limit parameters.
type Config struct {
	// FailOpen allows requests through when Redis is unavailable.
	FailOpen bool
}

// NewLimiter creates a redis_rate.Limiter from a Redis client.
func NewLimiter(redisClient redis.UniversalClient) *redis_rate.Limiter {
	return redis_rate.NewLimiter(redisClient)
}

// resolveLimit returns the rate limit for the current request, checking the
// RateLimitConfigFlag. The flag JSON format is:
//
//	{
//	  "/sandboxes/": {"rate": 50, "burst": 100, "period_s": 1},
//	  "/sandboxes/:sandboxID/pause": {"rate": 10, "burst": 20, "period_s": 60}
//	}
//
// period_s is optional and defaults to 1 (second).
// The route is the Gin route pattern (c.FullPath()). If no config exists
// for the route (or the flag is null), returns false (no limit applied).
func resolveLimit(ctx context.Context, ff *featureflags.Client, route string) (redis_rate.Limit, bool) {
	flagValue := ff.JSONFlag(ctx, featureflags.RateLimitConfigFlag)
	if flagValue.IsNull() {
		return redis_rate.Limit{}, false
	}

	override := flagValue.GetByKey(route)
	if override.IsNull() {
		return redis_rate.Limit{}, false
	}

	rate := override.GetByKey("rate")
	burst := override.GetByKey("burst")

	if !rate.IsInt() || !burst.IsInt() {
		return redis_rate.Limit{}, false
	}

	period := time.Second
	if v := override.GetByKey("period_s"); v.IsInt() {
		period = time.Duration(v.IntValue()) * time.Second
	}

	return redis_rate.Limit{
		Rate:   rate.IntValue(),
		Burst:  burst.IntValue(),
		Period: period,
	}, true
}

func Middleware(limiter *redis_rate.Limiter, cfg Config, ff *featureflags.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()

		// Skip unauthenticated requests
		team, ok := auth.GetTeamInfo(c)
		if !ok {
			c.Next()

			return
		}

		route := c.FullPath()

		// Resolve per-team limit overrides from feature flag.
		// If the route is not configured, allow the request through (no limit).
		limit, ok := resolveLimit(ctx, ff, route)
		if !ok {
			c.Next()

			return
		}

		// Build a logger with rate limit context for reuse.
		teamID := team.ID.String()
		l := logger.L().With(
			logger.WithTeamID(teamID),
			zap.String("route", route),
			zap.Int("rate_limit_rate", limit.Rate),
			zap.Int("rate_limit_burst", limit.Burst),
		)

		key := redis_utils.CreateKey(rateLimitPefix, teamID, route)
		res, err := limiter.Allow(ctx, key, limit)
		if err != nil {
			l.Warn(ctx, "rate limiter Redis error", zap.Error(err))

			if cfg.FailOpen {
				c.Next()

				return
			}

			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
				"code":    http.StatusInternalServerError,
				"message": "Rate limiter unavailable",
			})

			return
		}

		// Set standard rate limit headers
		c.Header("RateLimit-Limit", strconv.Itoa(limit.Burst))
		c.Header("RateLimit-Remaining", strconv.Itoa(res.Remaining))
		c.Header("RateLimit-Reset", strconv.FormatInt(int64(math.Ceil(res.ResetAfter.Seconds())), 10))

		if res.Allowed > 0 {
			c.Next()

			return
		}

		// Denied — set Retry-After and return 429.
		retryAfterSecs := max(int(res.RetryAfter.Seconds()), 1)
		c.Header("Retry-After", strconv.Itoa(retryAfterSecs))

		l.Warn(ctx, "rate limit exceeded",
			zap.Int("remaining", res.Remaining),
			zap.Int("retry_after_s", retryAfterSecs),
		)

		c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
			"code":    http.StatusTooManyRequests,
			"message": "Rate limit exceeded",
		})
	}
}
