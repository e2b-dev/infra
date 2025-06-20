package env

import (
	"os"
	"strconv"
)

var environment = GetEnv("ENVIRONMENT", "prod")

func IsLocal() bool {
	return environment == "local"
}

func IsDevelopment() bool {
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
