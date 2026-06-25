//go:build linux

package cfg

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestServicesCapabilities(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name               string
		services           Services
		runsOrchestrator   bool
		runsTemplateMgr    bool
		usesSandboxRuntime bool
	}{
		{
			name:               "orchestrator",
			services:           Services{Orchestrator},
			runsOrchestrator:   true,
			usesSandboxRuntime: true,
		},
		{
			name:               "template manager",
			services:           Services{TemplateManager},
			runsTemplateMgr:    true,
			usesSandboxRuntime: true,
		},
		{
			name:               "combined",
			services:           Services{Orchestrator, TemplateManager},
			runsOrchestrator:   true,
			runsTemplateMgr:    true,
			usesSandboxRuntime: true,
		},
		{
			name:     "unknown only",
			services: Services{UnknownService},
		},
		{
			name: "empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.runsOrchestrator, tt.services.RunsOrchestrator())
			assert.Equal(t, tt.runsTemplateMgr, tt.services.RunsTemplateManager())
			assert.Equal(t, tt.usesSandboxRuntime, tt.services.UsesSandboxRuntime())
		})
	}
}

func TestGetServicesFiltersUnknownServices(t *testing.T) {
	t.Parallel()

	services := GetServices(Config{Services: []string{" orchestrator ", "unknown", "TEMPLATE-MANAGER"}})

	assert.Equal(t, Services{Orchestrator, TemplateManager}, services)
}
