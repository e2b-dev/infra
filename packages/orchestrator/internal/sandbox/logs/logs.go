package logs

import (
	"errors"
	"os"
	"strings"
)

type LogsConfiguration struct {
	Endpoint string // can be domain or IP address
	Cidr     string // CIDR that will be allowed in sandbox network
}

var (
	publicIpCollector = os.Getenv("LOGS_COLLECTOR_PUBLIC_IP")

	logsCollectorCidr     = os.Getenv("SANDBOX_LOGS_COLLECTOR_CIDR")
	logsCollectorEndpoint = os.Getenv("SANDBOX_LOGS_COLLECTOR_ADDRESS")
)

func GetSandboxLogsConfiguration() (*LogsConfiguration, error) {
	// legacy configuration with public IPv4 address
	if publicIpCollector != "" {
		logsConfig := &LogsConfiguration{
			Endpoint: publicIpCollector,
			Cidr:     strings.TrimPrefix(publicIpCollector, "http://") + "/32", // single IP in CIDR notation
		}

		return logsConfig, nil
	}

	if logsCollectorEndpoint != "" && logsCollectorCidr != "" {
		logsConfig := &LogsConfiguration{
			Endpoint: logsCollectorEndpoint,
			Cidr:     logsCollectorCidr,
		}

		return logsConfig, nil
	}

	return nil, errors.New("no logs collector configuration found")
}
