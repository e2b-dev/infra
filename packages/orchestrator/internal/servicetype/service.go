package servicetype

import (
	"strings"

	"github.com/e2b-dev/infra/packages/shared/pkg/env"
)

type ServiceType string

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
func GetServices() []ServiceType {
	servicesEnv := env.GetEnv("ORCHESTRATOR_SERVICES", string(Orchestrator))
	rawServiceNames := strings.Split(servicesEnv, ",")

	var services []ServiceType
	for _, name := range rawServiceNames {
		service := ParseServiceType(name)
		if service != UnknownService {
			services = append(services, service)
		}
	}

	return services
}

// GetServiceName returns a single string identifier for the given services.
// If multiple services are present, they are joined with underscores.
func GetServiceName(services []ServiceType) string {
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
