package memory

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"

	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
)

func createFilterTestSandbox(state sandbox.State, endTime time.Time) sandbox.Sandbox {
	return sandbox.Sandbox{
		SandboxID:         "test-sandbox-id",
		TemplateID:        "test-template",
		ClientID:          "test-client",
		ExecutionID:       "test-execution",
		TeamID:            uuid.New(),
		BuildID:           uuid.New(),
		ClusterID:         uuid.New(),
		MaxInstanceLength: time.Hour,
		StartTime:         time.Now().Add(-30 * time.Minute),
		EndTime:           endTime,
		State:             state,
		AutoPause:         false,
	}
}

// TestApplyFilter_ExpiredFiltering tests the expiration filter logic used by the evictor
func TestApplyFilter_ExpiredFiltering(t *testing.T) {
	tests := []struct {
		name          string
		onlyExpired   bool
		endTime       time.Time
		expectedMatch bool
		description   string
	}{
		{
			name:          "no filter - not expired",
			onlyExpired:   false,
			endTime:       time.Now().Add(time.Hour),
			expectedMatch: true,
			description:   "Should match when no filter is applied",
		},
		{
			name:          "no filter - expired",
			onlyExpired:   false,
			endTime:       time.Now().Add(-time.Hour),
			expectedMatch: true,
			description:   "Should match when no filter is applied, even if expired",
		},
		{
			name:          "expired filter - not expired",
			onlyExpired:   true,
			endTime:       time.Now().Add(time.Hour),
			expectedMatch: false,
			description:   "Should NOT match when filtering for expired but sandbox is active",
		},
		{
			name:          "expired filter - expired",
			onlyExpired:   true,
			endTime:       time.Now().Add(-time.Hour),
			expectedMatch: true,
			description:   "Should match when filtering for expired and sandbox is expired",
		},
		{
			name:          "expired filter - just expired",
			onlyExpired:   true,
			endTime:       time.Now().Add(-time.Nanosecond),
			expectedMatch: true,
			description:   "Should match when expired by even a nanosecond",
		},
		{
			name:          "expired filter - about to expire",
			onlyExpired:   true,
			endTime:       time.Now().Add(time.Millisecond),
			expectedMatch: false,
			description:   "Should NOT match when still has time left",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filter := sandbox.NewItemsFilter()
			sandbox.WithOnlyExpired(tt.onlyExpired)(filter)

			sbx := createFilterTestSandbox(sandbox.StateRunning, tt.endTime)
			result := applyFilter(sbx, filter)

			assert.Equal(t, tt.expectedMatch, result, tt.description)
		})
	}
}
