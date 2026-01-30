package env

import (
	"os"
	"strconv"

	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

var environment = GetEnv("ENVIRONMENT", "prod")

func IsLocal() bool {
	return environment == "local"
}

func IsDevelopment() bool {
	return environment == "dev" || environment == "local"
}

func IsDebug() bool {
	// Auto-enable debug logging in dev/local environments
	return IsDevelopment() || GetEnv("E2B_DEBUG", "false") == "true"
}

func GetEnv(key, defaultValue string) string {
	value := os.Getenv(key)
	if len(value) == 0 {
		return defaultValue
	}

	return value
}

func GetEnvAsInt(key string, defaultValue int) (int, error) {
	if v := os.Getenv(key); v != "" {
		value, err := strconv.Atoi(v)
		if err != nil {
			return defaultValue, err
		}

		return value, nil
	}

	return defaultValue, nil
}

func GetNodeID() string {
	return utils.RequiredEnv("NODE_ID", "Node ID of the instance node is required")
}

func GetNodeIP() string {
	return utils.RequiredEnv("NODE_IP", "Node IP of the instance node is required")
}

func LogsCollectorAddress() string {
	return os.Getenv("LOGS_COLLECTOR_ADDRESS")
}
