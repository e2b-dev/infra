package redis_utils

import "fmt"

const lockKeyPrefix = "lock:"

func GetLockKey(key string) string {
	return fmt.Sprintf("%s%s", lockKeyPrefix, key)
}
