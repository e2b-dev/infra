package env

import (
	"os"
	"strconv"

	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

func IsLocal() bool {
	return GetEnv("ENVIRONMENT", "prod") == "local"
}

func IsDevelopment() bool {
	environment := GetEnv("ENVIRONMENT", "prod")

	return environment == "dev" || environment == "local"
}

func IsDebug() bool {
	return GetEnv("E2B_DEBUG", "false") == "true"
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
