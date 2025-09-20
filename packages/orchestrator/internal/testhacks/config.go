package testhacks

import "os"

func IsTesting() bool {
	return os.Getenv("CI") == "true"
}
