//go:build linux

package cfg

import (
	"slices"
	"strings"
)

type ServiceType string

type Services []ServiceType

const (
	UnknownService  ServiceType = "orch-unknown"
	Orchestrator    ServiceType = "orchestrator"
	TemplateManager ServiceType = "template-manager"
)

// ParseServiceType converts a string to a ServiceType.
// It is case-insensitive and defaults to UnknownService.
func ParseServiceType(s string) ServiceType {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case string(Orchestrator):
		return Orchestrator
	case string(TemplateManager):
		return TemplateManager
	default:
		return UnknownService
	}
}

// GetServices parses the ORCHESTRATOR_SERVICES environment variable
// and returns a slice of known ServiceTypes.
func GetServices(config Config) Services {
	rawServiceNames := config.Services

	services := make(Services, 0, len(rawServiceNames))
	for _, name := range rawServiceNames {
		service := ParseServiceType(name)
		if service != UnknownService {
			services = append(services, service)
		}
	}

	return services
}

func (s Services) Has(service ServiceType) bool {
	return slices.Contains(s, service)
}

func (s Services) RunsOrchestrator() bool {
	return s.Has(Orchestrator)
}

func (s Services) RunsTemplateManager() bool {
	return s.Has(TemplateManager)
}

func (s Services) UsesSandboxRuntime() bool {
	return s.RunsOrchestrator() || s.RunsTemplateManager()
}

// GetServiceName returns a single string identifier for the given services.
// If multiple services are present, they are joined with underscores.
func GetServiceName(services Services) string {
	if len(services) == 0 {
		return string(UnknownService)
	}

	var builder strings.Builder
	for i, s := range services {
		if i > 0 {
			builder.WriteString("_")
		}
		builder.WriteString(string(s))
	}

	return builder.String()
}
