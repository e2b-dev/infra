package team

import (
	"fmt"
	"net/http"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/constants"
	"github.com/e2b-dev/infra/packages/api/internal/db/types"
)

func LimitResources(limits *types.TeamLimits, cpuCount, memoryMB *int32) (int64, int64, *api.APIError) {
	cpu := constants.DefaultTemplateCPU
	ramMB := constants.DefaultTemplateMemory

	if cpuCount != nil {
		cpu = int64(*cpuCount)
		if cpu < constants.MinTemplateCPU {
			return 0, 0, &api.APIError{
				Err:       fmt.Errorf("CPU count must be at least %d", constants.MinTemplateCPU),
				ClientMsg: fmt.Sprintf("CPU count must be at least %d", constants.MinTemplateCPU),
				Code:      http.StatusBadRequest,
			}
		}

		if cpu > constants.MaxTemplateCPU {
			return 0, 0, &api.APIError{
				Err:       fmt.Errorf("CPU count must be at most %d", constants.MaxTemplateCPU),
				ClientMsg: fmt.Sprintf("CPU count must be at most %d", constants.MaxTemplateCPU),
				Code:      http.StatusBadRequest,
			}
		}

		if cpu != 1 && cpu%2 != 0 {
			return 0, 0, &api.APIError{
				Err:       fmt.Errorf("CPU count must be 1 or an even number"),
				ClientMsg: "CPU count must be 1 or an even number",
				Code:      http.StatusBadRequest,
			}
		}

		if cpu > limits.MaxVcpu {
			return 0, 0, &api.APIError{
				Err:       fmt.Errorf("CPU count exceeds team limits (%d)", limits.MaxVcpu),
				ClientMsg: fmt.Sprintf("CPU count can't be higher than %d (if you need to increase this limit, please contact support)", limits.MaxVcpu),
				Code:      http.StatusBadRequest,
			}
		}
	}

	if memoryMB != nil {
		ramMB = int64(*memoryMB)

		if ramMB < constants.MinTemplateMemory {
			return 0, 0, &api.APIError{
				Err:       fmt.Errorf("memory must be at least %d MiB", constants.MinTemplateMemory),
				ClientMsg: fmt.Sprintf("Memory must be at least %d MiB", constants.MinTemplateMemory),
				Code:      http.StatusBadRequest,
			}
		}

		if ramMB%2 != 0 {
			return 0, 0, &api.APIError{
				Err:       fmt.Errorf("user provided memory size isn't divisible by 2"),
				ClientMsg: "Memory must be divisible by 2",
				Code:      http.StatusBadRequest,
			}
		}

		if ramMB > limits.MaxRamMb {
			return 0, 0, &api.APIError{
				Err:       fmt.Errorf("memory exceeds team limits (%d MiB)", limits.MaxRamMb),
				ClientMsg: fmt.Sprintf("Memory can't be higher than %d MiB (if you need to increase this limit, please contact support)", limits.MaxRamMb),
				Code:      http.StatusBadRequest,
			}
		}
	}

	return cpu, ramMB, nil
}
