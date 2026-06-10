package affinity

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	redis_utils "github.com/e2b-dev/infra/packages/shared/pkg/redis"
)

func testConfig() Config {
	return Config{
		Enabled:      true,
		TTL:          time.Hour,
		TopNodes:     20,
		ReadTimeout:  time.Second,
		WriteTimeout: time.Second,
		MaxHits:      10,
		Weight:       0.002,
		MaxBonus:     0.02,
	}
}

func meta(buildID string) *orchestrator.SchedulingMetadata {
	return &orchestrator.SchedulingMetadata{BuildId: buildID}
}

func TestRecordAndScores(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	idx := NewIndex(redis_utils.SetupInstance(t))
	cfg := testConfig()

	t.Run("exact hit", func(t *testing.T) {
		t.Parallel()
		cluster := uuid.New()
		idx.Record(ctx, cfg, cluster, "node-1", meta("build-1"))

		scores := idx.Scores(ctx, cfg, cluster, "build-1")
		require.NotNil(t, scores)
		assert.InDelta(t, cfg.Weight, scores["node-1"], 1e-9)

		assert.Nil(t, idx.Scores(ctx, cfg, cluster, "unknown-build"))
		assert.Nil(t, idx.Scores(ctx, cfg, uuid.New(), "build-1"), "clusters are isolated")
	})

	t.Run("hit clamp and max bonus", func(t *testing.T) {
		t.Parallel()
		cluster := uuid.New()
		for range 30 {
			idx.Record(ctx, cfg, cluster, "node-1", meta("build-1"))
		}

		scores := idx.Scores(ctx, cfg, cluster, "build-1")
		require.NotNil(t, scores)
		assert.InDelta(t, cfg.MaxHits*cfg.Weight, scores["node-1"], 1e-9)
		assert.LessOrEqual(t, scores["node-1"], cfg.MaxBonus)
	})

	t.Run("top nodes cap", func(t *testing.T) {
		t.Parallel()
		cluster := uuid.New()
		capped := cfg
		capped.TopNodes = 5
		for n := range 25 {
			idx.Record(ctx, capped, cluster, "node-"+string(rune('a'+n)), meta("build-1"))
		}

		scores := idx.Scores(ctx, capped, cluster, "build-1")
		require.NotNil(t, scores)
		assert.LessOrEqual(t, len(scores), 5)
	})
}
