package team

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/api/internal/constants"
	"github.com/e2b-dev/infra/packages/api/internal/db/types"
)

func TestLimitResources(t *testing.T) {
	t.Parallel()

	defaultLimits := &types.TeamLimits{
		MaxVcpu:  32,
		MaxRamMb: 8192,
	}

	t.Run("valid CPU counts", func(t *testing.T) {
		t.Parallel()
		validCPUs := []int32{1, 2, 4, 8, 16, 32}
		for _, cpuCount := range validCPUs {
			cpu := cpuCount
			cpuResult, _, apiErr := LimitResources(defaultLimits, &cpu, nil)
			require.Nil(t, apiErr, "CPU count %d should be valid", cpu)
			assert.Equal(t, int64(cpu), cpuResult)
		}
	})

	t.Run("invalid CPU count - odd number greater than 1", func(t *testing.T) {
		t.Parallel()
		invalidCPUs := []int32{3, 5, 7, 9, 31}
		for _, cpuCount := range invalidCPUs {
			cpu := cpuCount
			_, _, apiErr := LimitResources(defaultLimits, &cpu, nil)
			require.NotNil(t, apiErr, "CPU count %d should be invalid", cpu)
			assert.Equal(t, http.StatusBadRequest, apiErr.Code)
			assert.Contains(t, apiErr.ClientMsg, "CPU count must be 1 or an even number")
		}
	})

	t.Run("invalid CPU count - exceeds Firecracker max", func(t *testing.T) {
		t.Parallel()
		highLimits := &types.TeamLimits{
			MaxVcpu:  100,
			MaxRamMb: 8192,
		}
		invalidCPUs := []int32{33, 34, 64, 100}
		for _, cpuCount := range invalidCPUs {
			cpu := cpuCount
			_, _, apiErr := LimitResources(highLimits, &cpu, nil)
			require.NotNil(t, apiErr, "CPU count %d should be invalid", cpu)
			assert.Equal(t, http.StatusBadRequest, apiErr.Code)
			assert.Contains(t, apiErr.ClientMsg, "CPU count must be at most 32")
		}
	})

	t.Run("invalid CPU count - below minimum", func(t *testing.T) {
		t.Parallel()
		cpu := int32(0)
		_, _, apiErr := LimitResources(defaultLimits, &cpu, nil)
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusBadRequest, apiErr.Code)
		assert.Contains(t, apiErr.ClientMsg, "CPU count must be at least 1")
	})

	t.Run("invalid CPU count - exceeds team limit", func(t *testing.T) {
		t.Parallel()
		lowLimits := &types.TeamLimits{
			MaxVcpu:  8,
			MaxRamMb: 8192,
		}
		cpu := int32(16)
		_, _, apiErr := LimitResources(lowLimits, &cpu, nil)
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusBadRequest, apiErr.Code)
		assert.Contains(t, apiErr.ClientMsg, "CPU count can't be higher than 8")
	})

	t.Run("defaults when CPU is nil", func(t *testing.T) {
		t.Parallel()
		cpuResult, _, apiErr := LimitResources(defaultLimits, nil, nil)
		require.Nil(t, apiErr)
		assert.Equal(t, constants.DefaultTemplateCPU, cpuResult)
	})

	t.Run("valid memory", func(t *testing.T) {
		t.Parallel()
		validMemory := []int32{128, 256, 512, 1024, 2048}
		for _, memMB := range validMemory {
			mem := memMB
			_, memResult, apiErr := LimitResources(defaultLimits, nil, &mem)
			require.Nil(t, apiErr, "Memory %d should be valid", mem)
			assert.Equal(t, int64(mem), memResult)
		}
	})

	t.Run("invalid memory - not divisible by 2", func(t *testing.T) {
		t.Parallel()
		mem := int32(129)
		_, _, apiErr := LimitResources(defaultLimits, nil, &mem)
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusBadRequest, apiErr.Code)
		assert.Contains(t, apiErr.ClientMsg, "Memory must be divisible by 2")
	})

	t.Run("invalid memory - below minimum", func(t *testing.T) {
		t.Parallel()
		mem := int32(64)
		_, _, apiErr := LimitResources(defaultLimits, nil, &mem)
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusBadRequest, apiErr.Code)
		assert.Contains(t, apiErr.ClientMsg, "Memory must be at least 128 MiB")
	})

	t.Run("invalid memory - exceeds team limit", func(t *testing.T) {
		t.Parallel()
		mem := int32(16384)
		_, _, apiErr := LimitResources(defaultLimits, nil, &mem)
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusBadRequest, apiErr.Code)
		assert.Contains(t, apiErr.ClientMsg, "Memory can't be higher than 8192 MiB")
	})

	t.Run("defaults when memory is nil", func(t *testing.T) {
		t.Parallel()
		_, memResult, apiErr := LimitResources(defaultLimits, nil, nil)
		require.Nil(t, apiErr)
		assert.Equal(t, constants.DefaultTemplateMemory, memResult)
	})
}
