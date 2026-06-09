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
		Enabled:            true,
		TTL:                time.Hour,
		TopNodes:           20,
		ReadTimeout:        time.Second,
		WriteTimeout:       time.Second,
		MaxHits:            10,
		ExactWeight:        0.002,
		BaseWeight:         0.001,
		LineageWeight:      0.0005,
		MaxBonus:           0.02,
		MaxLineageRecorded: 16,
	}
}

func testMeta() *orchestrator.SchedulingMetadata {
	return &orchestrator.SchedulingMetadata{
		BuildId:            "build-current",
		MemfileBaseBuildId: "build-base",
		RootfsBaseBuildId:  "build-base",
		MemfileBuildIds:    []string{"build-base", "build-current", "lin-mem"},
		MemfileBuildBytes:  []uint64{100, 200, 50},
		RootfsBuildIds:     []string{"build-base", "build-current", "lin-rootfs"},
		RootfsBuildBytes:   []uint64{100, 200, 25},
	}
}

func TestRecordAndScores(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	idx := NewIndex(redis_utils.SetupInstance(t))
	cfg := testConfig()

	t.Run("tiers", func(t *testing.T) {
		t.Parallel()
		cluster := uuid.New()
		idx.Record(ctx, cfg, cluster, "node-1", testMeta())

		exact := idx.Scores(ctx, cfg, cluster, "build-current")
		base := idx.Scores(ctx, cfg, cluster, "build-base")
		lineage := idx.Scores(ctx, cfg, cluster, "lin-mem")

		require.NotNil(t, exact)
		require.NotNil(t, base)
		require.NotNil(t, lineage)
		assert.InDelta(t, cfg.ExactWeight, exact["node-1"], 1e-9)
		assert.InDelta(t, cfg.BaseWeight, base["node-1"], 1e-9)
		assert.InDelta(t, cfg.LineageWeight, lineage["node-1"], 1e-9)

		assert.Nil(t, idx.Scores(ctx, cfg, cluster, "unknown-build"))
		assert.Nil(t, idx.Scores(ctx, cfg, uuid.New(), "build-current"), "clusters are isolated")
	})

	t.Run("hit clamp and max bonus", func(t *testing.T) {
		t.Parallel()
		cluster := uuid.New()
		for range 30 {
			idx.Record(ctx, cfg, cluster, "node-1", testMeta())
		}

		scores := idx.Scores(ctx, cfg, cluster, "build-current")
		require.NotNil(t, scores)
		assert.LessOrEqual(t, scores["node-1"], cfg.MaxBonus)
		assert.InDelta(t, cfg.MaxHits*cfg.ExactWeight, scores["node-1"], 1e-9)
	})

	t.Run("top nodes cap", func(t *testing.T) {
		t.Parallel()
		cluster := uuid.New()
		capped := cfg
		capped.TopNodes = 5
		for n := range 25 {
			idx.Record(ctx, capped, cluster, "node-"+string(rune('a'+n)), testMeta())
		}

		scores := idx.Scores(ctx, capped, cluster, "build-current")
		require.NotNil(t, scores)
		assert.LessOrEqual(t, len(scores), 5)
	})
}

func TestLineageBuilds_CapPrefersHeaviest(t *testing.T) {
	t.Parallel()
	meta := &orchestrator.SchedulingMetadata{
		BuildId:            "build",
		MemfileBaseBuildId: "base",
		RootfsBaseBuildId:  "base",
		MemfileBuildIds:    []string{"base", "build", "lin-heavy", "lin-light"},
		MemfileBuildBytes:  []uint64{1, 1, 1000, 1},
		RootfsBuildIds:     []string{"base", "build", "lin-medium"},
		RootfsBuildBytes:   []uint64{1, 1, 100},
	}

	got := lineageBuilds(meta, 2, []string{"base", "build"})
	assert.Equal(t, []string{"lin-heavy", "lin-medium"}, got)
}
