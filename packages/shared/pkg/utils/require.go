package utils

import (
	"fmt"
	"os"
)

// RequireEnv panics if the environment variable is not set or is empty.
// Pass the usageMsg to describe how the environment variable is used. This will be used in the error message.
func RequireEnv(key string, usageMsg string) string {
	value, ok := os.LookupEnv(key)
	if !ok {
		panic(fmt.Sprintf("Environment variable \"%s\" (%s) is not set. Please set it to a non-empty value.", key, usageMsg))
	}

	if value == "" {
		panic(fmt.Sprintf("Environment variable \"%s\" (%s) is set but it is empty. Please set it to a non-empty value.", key, usageMsg))
	}

	return value
}
