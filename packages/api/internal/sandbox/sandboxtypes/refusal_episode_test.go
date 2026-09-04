package sandboxtypes

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestRefusalEpisodeStart(t *testing.T) {
	t.Parallel()

	now := time.Now()

	assert.Equal(t, now, Sandbox{}.RefusalEpisodeStart(now), "never refused: the episode starts now")

	alive := Sandbox{RefusedSince: now.Add(-time.Minute), RefusedUntil: now.Add(-5 * time.Second)}
	assert.Equal(t, alive.RefusedSince, alive.RefusalEpisodeStart(now), "the window ended seconds ago: same episode")

	edge := Sandbox{RefusedSince: now.Add(-time.Minute), RefusedUntil: now.Add(-RefusalEpisodeGap)}
	assert.Equal(t, edge.RefusedSince, edge.RefusalEpisodeStart(now), "exactly at the gap: still the same episode")

	stale := Sandbox{RefusedSince: now.Add(-3 * time.Hour), RefusedUntil: now.Add(-3*time.Hour + 10*time.Second)}
	assert.Equal(t, now, stale.RefusalEpisodeStart(now), "a refusal hours after the window: a new episode")
}
