package orchestrator

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
)

func TestGetRoutingCatalogTTL(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 7, 12, 0, 0, 0, time.UTC)

	t.Run("uses remaining end time", func(t *testing.T) {
		t.Parallel()

		sbx := sandbox.Sandbox{
			EndTime: now.Add(45 * time.Second),
		}

		assert.Equal(t, 45*time.Second, getRoutingCatalogTTL(now, sbx))
	})

	t.Run("clamps expired sandboxes to minimal ttl", func(t *testing.T) {
		t.Parallel()

		sbx := sandbox.Sandbox{
			EndTime: now.Add(-time.Second),
		}

		assert.Equal(t, time.Millisecond, getRoutingCatalogTTL(now, sbx))
	})
}
