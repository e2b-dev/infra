package servicediscovery

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Consumers bind these tags under their own prefix (SD_, SD_ORCHESTRATOR_) and
// nothing in this package parses them, so a rename is invisible: K8S_API_ENDPOINT
// would stay empty and the backend would silently take the in-cluster branch.
func TestConfig_EnvNamesAreTheDocumentedOnes(t *testing.T) {
	t.Parallel()

	got := map[string]string{}
	for field := range reflect.TypeFor[Config]().Fields() {
		got[field.Name] = field.Tag.Get("env")
	}

	assert.Equal(t, map[string]string{
		"Provider":           "PROVIDER,required",
		"OrchestratorPort":   "PORT",
		"DNSQuery":           "DNS_QUERY",
		"DNSResolverAddress": "DNS_RESOLVER_ADDRESS",
		"K8sAPIEndpoint":     "K8S_API_ENDPOINT",
		"PodNamespace":       "POD_NAMESPACE",
		"PodLabels":          "POD_LABELS",
		"HostIP":             "HOST_IP",
		"StaticEndpoints":    "STATIC",
		"NomadEndpoint":      "NOMAD_ENDPOINT",
		"NomadToken":         "NOMAD_TOKEN",
	}, got)
}
