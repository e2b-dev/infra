package ratelimit

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis_rate/v10"
	"github.com/google/uuid"
	"github.com/launchdarkly/go-sdk-common/v3/ldvalue"
	"github.com/launchdarkly/go-server-sdk/v7/testhelpers/ldtestdata"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/auth/pkg/auth"
	"github.com/e2b-dev/infra/packages/auth/pkg/types"
	authqueries "github.com/e2b-dev/infra/packages/db/pkg/auth/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
	redis_utils "github.com/e2b-dev/infra/packages/shared/pkg/redis"
)

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	m.Run()
}

// newTestFF creates a feature flags client with optional route config overrides.
func newTestFF(t *testing.T, routeConfigs ...map[string]map[string]int) *featureflags.Client {
	t.Helper()

	td := ldtestdata.DataSource()

	if len(routeConfigs) > 0 && routeConfigs[0] != nil {
		td.Update(td.Flag(featureflags.RateLimitConfigFlag.Key()).ValueForAll(ldvalue.CopyArbitraryValue(routeConfigs[0])))
	}

	ff, err := featureflags.NewClientWithDatasource(td)
	require.NoError(t, err)

	t.Cleanup(func() {
		_ = ff.Close(t.Context())
	})

	return ff
}

const testRoute = "/sandboxes/:sandboxID/connect"

// routeConfig returns a route config map for the test route with the given rate and burst.
func routeConfig(rate, burst int) map[string]map[string]int {
	return map[string]map[string]int{
		testRoute: {"rate": rate, "burst": burst},
	}
}

// doRequest performs a POST /sandboxes/test-sbx/connect.
func doRequest(r *gin.Engine) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, "/sandboxes/test-sbx/connect", nil)
	r.ServeHTTP(w, req)

	return w
}

// newRouterWithTeam creates a Gin engine that injects a team then applies rate limiting.
func newRouterWithTeam(limiter *redis_rate.Limiter, cfg Config, ff *featureflags.Client, teamID uuid.UUID) *gin.Engine {
	r := gin.New()
	r.Use(func(c *gin.Context) {
		auth.SetTeamInfo(c, &types.Team{
			Team: &authqueries.Team{ID: teamID},
		})
		c.Next()
	})
	r.Use(Middleware(limiter, cfg, ff))
	r.POST("/sandboxes/:sandboxID/connect", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	return r
}

// --- Unit tests ---

func TestMiddleware_SkipsUnauthenticated(t *testing.T) {
	t.Parallel()

	ff := newTestFF(t)
	// Unreachable Redis — shouldn't matter since no team is set.
	badClient := redis.NewClient(&redis.Options{Addr: "localhost:1"})
	defer badClient.Close()

	limiter := redis_rate.NewLimiter(badClient)

	r := gin.New()
	// No team set — unauthenticated.
	r.Use(Middleware(limiter, Config{FailOpen: true}, ff))
	r.POST("/sandboxes/:sandboxID/connect", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	w := doRequest(r)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestMiddleware_FailOpen(t *testing.T) {
	t.Parallel()

	ff := newTestFF(t, routeConfig(10, 10))
	// Unreachable Redis.
	badClient := redis.NewClient(&redis.Options{
		Addr:        "localhost:1",
		DialTimeout: 10 * time.Millisecond,
	})
	defer badClient.Close()

	limiter := redis_rate.NewLimiter(badClient)
	r := newRouterWithTeam(limiter, Config{FailOpen: true}, ff, uuid.New())

	w := doRequest(r)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestMiddleware_FailClosed(t *testing.T) {
	t.Parallel()

	ff := newTestFF(t, routeConfig(10, 10))
	badClient := redis.NewClient(&redis.Options{
		Addr:        "localhost:1",
		DialTimeout: 10 * time.Millisecond,
	})
	defer badClient.Close()

	limiter := redis_rate.NewLimiter(badClient)
	r := newRouterWithTeam(limiter, Config{FailOpen: false}, ff, uuid.New())

	w := doRequest(r)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestMiddleware_UnconfiguredRouteAllowsThrough(t *testing.T) {
	t.Parallel()

	// Rate limiting enabled, but no route config — all routes should pass through.
	ff := newTestFF(t)
	badClient := redis.NewClient(&redis.Options{Addr: "localhost:1"})
	defer badClient.Close()

	limiter := redis_rate.NewLimiter(badClient)
	r := newRouterWithTeam(limiter, Config{FailOpen: true}, ff, uuid.New())

	w := doRequest(r)
	assert.Equal(t, http.StatusOK, w.Code)
	// No rate limit headers should be set for unconfigured routes.
	assert.Empty(t, w.Header().Get("RateLimit-Limit"))
}

// --- Integration tests (real Redis) ---

func TestIntegration_AllowedRequestSetsHeaders(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("skipping integration test")
	}

	redisClient := redis_utils.SetupInstance(t)
	limiter := redis_rate.NewLimiter(redisClient)
	ff := newTestFF(t, routeConfig(10, 20))

	r := newRouterWithTeam(limiter, Config{FailOpen: true}, ff, uuid.New())

	w := doRequest(r)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "20", w.Header().Get("RateLimit-Limit"))
	assert.NotEmpty(t, w.Header().Get("RateLimit-Remaining"))
	assert.NotEmpty(t, w.Header().Get("RateLimit-Reset"))
}

func TestIntegration_BurstThenDeny(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("skipping integration test")
	}

	redisClient := redis_utils.SetupInstance(t)
	limiter := redis_rate.NewLimiter(redisClient)
	ff := newTestFF(t, routeConfig(1, 3))

	r := newRouterWithTeam(limiter, Config{FailOpen: true}, ff, uuid.New())

	// First 3 requests should succeed (burst).
	for i := range 3 {
		w := doRequest(r)
		assert.Equal(t, http.StatusOK, w.Code, "request %d should be allowed", i+1)
	}

	// 4th should be denied.
	w := doRequest(r)
	assert.Equal(t, http.StatusTooManyRequests, w.Code)
	assert.NotEmpty(t, w.Header().Get("Retry-After"))

	var body struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	err := json.NewDecoder(w.Body).Decode(&body)
	require.NoError(t, err)
	assert.Equal(t, http.StatusTooManyRequests, body.Code)
	assert.Equal(t, "Rate limit exceeded", body.Message)
}

func TestIntegration_Refill(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("skipping integration test")
	}

	redisClient := redis_utils.SetupInstance(t)
	limiter := redis_rate.NewLimiter(redisClient)
	ff := newTestFF(t, routeConfig(10, 2))

	r := newRouterWithTeam(limiter, Config{FailOpen: true}, ff, uuid.New())

	// Exhaust burst.
	for range 2 {
		w := doRequest(r)
		assert.Equal(t, http.StatusOK, w.Code)
	}
	w := doRequest(r)
	assert.Equal(t, http.StatusTooManyRequests, w.Code)

	// Wait for refill (rate=10/s → one token every 100ms).
	time.Sleep(200 * time.Millisecond)

	w = doRequest(r)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestIntegration_IndependentTeams(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("skipping integration test")
	}

	redisClient := redis_utils.SetupInstance(t)
	limiter := redis_rate.NewLimiter(redisClient)
	ff := newTestFF(t, routeConfig(1, 1))

	cfg := Config{FailOpen: true}

	teamA := uuid.New()
	teamB := uuid.New()

	rA := newRouterWithTeam(limiter, cfg, ff, teamA)
	rB := newRouterWithTeam(limiter, cfg, ff, teamB)

	// Team A uses its quota.
	w := doRequest(rA)
	assert.Equal(t, http.StatusOK, w.Code)
	w = doRequest(rA)
	assert.Equal(t, http.StatusTooManyRequests, w.Code)

	// Team B should still have quota.
	w = doRequest(rB)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestIntegration_ConcurrentAccess(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("skipping integration test")
	}

	redisClient := redis_utils.SetupInstance(t)
	limiter := redis_rate.NewLimiter(redisClient)
	ff := newTestFF(t, map[string]map[string]int{
		testRoute: {"rate": 1, "burst": 10, "period_s": 600}, // slow refill so burst is the effective limit
	})

	burst := 10
	r := newRouterWithTeam(limiter, Config{FailOpen: true}, ff, uuid.New())

	// Fire 20 concurrent requests; only `burst` should be allowed.
	total := 20
	results := make([]int, total)

	var wg sync.WaitGroup
	for i := range total {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			w := doRequest(r)
			results[idx] = w.Code
		}(i)
	}
	wg.Wait()

	allowed := 0
	denied := 0
	for _, code := range results {
		switch code {
		case http.StatusOK:
			allowed++
		case http.StatusTooManyRequests:
			denied++
		default:
			t.Errorf("unexpected status code: %d", code)
		}
	}

	assert.Equal(t, burst, allowed, "exactly burst requests should be allowed")
	assert.Equal(t, total-burst, denied, "remaining requests should be denied")
}

func TestIntegration_DynamicPeriodUpdate(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("skipping integration test")
	}

	redisClient := redis_utils.SetupInstance(t)
	limiter := redis_rate.NewLimiter(redisClient)

	// Set up LD data source directly so we can update it mid-test.
	td := ldtestdata.DataSource()
	td.Update(td.Flag(featureflags.RateLimitConfigFlag.Key()).ValueForAll(
		ldvalue.CopyArbitraryValue(map[string]map[string]int{
			testRoute: {"rate": 100, "burst": 3, "period_s": 1},
		}),
	))

	ff, err := featureflags.NewClientWithDatasource(td)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ff.Close(t.Context()) })

	cfg := Config{FailOpen: true}

	// Phase 1: period_s=1 (fast refill). Use teamA for a fresh Redis key.
	rA := newRouterWithTeam(limiter, cfg, ff, uuid.New())

	for range 3 {
		w := doRequest(rA)
		assert.Equal(t, http.StatusOK, w.Code)
	}
	w := doRequest(rA)
	assert.Equal(t, http.StatusTooManyRequests, w.Code)

	// Wait for refill (rate=10/1s → one token every 100ms).
	time.Sleep(10 * time.Millisecond)
	w = doRequest(rA)
	assert.Equal(t, http.StatusOK, w.Code, "should refill quickly with period_s=1")

	// Wait for the phase 1 GCRA key to expire (TTL ≈ 1s) so phase 2
	// starts with a clean slate on the same team.
	time.Sleep(1100 * time.Millisecond)

	// Phase 2: update to period_s=600 (very slow refill), same team.
	td.Update(td.Flag(featureflags.RateLimitConfigFlag.Key()).ValueForAll(
		ldvalue.CopyArbitraryValue(map[string]map[string]int{
			testRoute: {"rate": 1, "burst": 3, "period_s": 600},
		}),
	))

	// Exhaust the full burst under the new period.
	for range 3 {
		w = doRequest(rA)
		assert.Equal(t, http.StatusOK, w.Code)
	}
	w = doRequest(rA)
	assert.Equal(t, http.StatusTooManyRequests, w.Code)

	// Wait a short time — should NOT refill with period_s=600
	// (rate=1/600s → one token every 600s).
	time.Sleep(100 * time.Millisecond)
	w = doRequest(rA)
	assert.Equal(t, http.StatusTooManyRequests, w.Code, "should not refill with period_s=600")
}
