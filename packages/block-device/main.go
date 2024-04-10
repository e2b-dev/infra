package main

import (
	"flag"

	dev "github.com/e2b-dev/infra/packages/block-device/internal"
)

var (
	socketPath string
	bucketName string
	bucketPath string
	// Should we get this from the backend dynamically
	size int64
)

func parseFlags() {
	flag.StringVar(&socketPath, "socket", "", "Path to the socket file")
	flag.StringVar(&bucketName, "bucket", "", "Path to the GCP bucket")
	flag.StringVar(&bucketPath, "path", "", "Path to the GCP object")
	flag.Int64Var(&size, "size", 0, "Size of the block device in bytes")

	flag.Parse()
}

func main() {
	parseFlags()

	device, err := dev.NewDevice(
		socketPath,
		bucketName,
		bucketPath,
		size,
	)
	if err != nil {
		panic(err)
	}

	// We want to elegantly pipe the read and write operations from the block devices through any caching, etc layers with a simple configuration.
}
