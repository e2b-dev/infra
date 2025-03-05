package env

import "os"

var environment = GetEnv("ENVIRONMENT", "local")

func IsProduction() bool {
	return environment == "prod"
}

func IsLocal() bool {
	return environment == "local"
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
