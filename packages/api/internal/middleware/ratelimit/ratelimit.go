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
	// Rate is the number of requests allowed per Period.
	Rate int
	// Burst is the maximum number of requests allowed in a single burst.
	Burst int
	// Period is the time window for the rate.
	Period time.Duration
	// FailOpen allows requests through when Redis is unavailable.
	FailOpen bool
}

// DefaultConfig returns a sensible default: 50 req/s with burst of 100.
func DefaultConfig() Config {
	return Config{
		Rate:     50,
		Burst:    100,
		Period:   time.Second,
		FailOpen: true,
	}
}

// NewLimiter creates a redis_rate.Limiter from a Redis client.
func NewLimiter(redisClient redis.UniversalClient) *redis_rate.Limiter {
	return redis_rate.NewLimiter(redisClient)
}

// Middleware returns a Gin middleware that enforces per-team rate limits
// using the GCRA algorithm backed by Redis (go-redis/redis_rate).
//
// The middleware is gated by the RateLimitEnabledFlag feature flag for
// gradual rollout. Unauthenticated requests are passed through.
// resolveLimit returns the rate limit for the current request, checking the
// RateLimitConfigFlag for per-team overrides. The flag JSON format is:
//
//	{
//	  "/sandboxes/": {"rate": 50, "burst": 100},
//	  "/sandboxes/:sandboxID/pause": {"rate": 10, "burst": 20}
//	}
//
// The route is the Gin route pattern (c.FullPath()). If no override exists
// for the route (or the flag is null), code defaults are used.
func resolveLimit(ctx context.Context, ff *featureflags.Client, cfg Config, route string) redis_rate.Limit {
	rate := cfg.Rate
	burst := cfg.Burst

	flagValue := ff.JSONFlag(ctx, featureflags.RateLimitConfigFlag)
	if !flagValue.IsNull() {
		override := flagValue.GetByKey(route)
		if !override.IsNull() {
			if v := override.GetByKey("rate"); v.IsInt() {
				rate = v.IntValue()
			}

			if v := override.GetByKey("burst"); v.IsInt() {
				burst = v.IntValue()
			}
		}
	}

	return redis_rate.Limit{
		Rate:   rate,
		Burst:  burst,
		Period: cfg.Period,
	}
}

func Middleware(limiter *redis_rate.Limiter, cfg Config, ff *featureflags.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()

		// Check feature flag — skip if rate limiting is disabled.
		if !ff.BoolFlag(ctx, featureflags.RateLimitEnabledFlag) {
			c.Next()

			return
		}

		// Skip unauthenticated requests (they'll be rejected by auth middleware).
		team, ok := auth.GetTeamInfo(c)
		if !ok {
			c.Next()

			return
		}

		teamID := team.ID.String()
		route := c.FullPath()
		key := redis_utils.CreateKey(rateLimitPefix, teamID, route)

		// Resolve per-team limit overrides from feature flag.
		limit := resolveLimit(ctx, ff, cfg, route)

		// Build a logger with rate limit context for reuse.
		l := logger.L().With(
			logger.WithTeamID(teamID),
			zap.String("route", route),
			zap.Int("rate_limit_rate", limit.Rate),
			zap.Int("rate_limit_burst", limit.Burst),
		)

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
