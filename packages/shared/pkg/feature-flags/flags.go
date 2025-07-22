package feature_flags

import (
	"runtime"

	"github.com/shirou/gopsutil/v4/mem"

	"github.com/e2b-dev/infra/packages/shared/pkg/env"
)

// Flag for enabling writing metrics for sandbox
// https://app.launchdarkly.com/projects/default/flags/sandbox-metrics-write
const (
	MetricsWriteFlagName = "sandbox-metrics-write"
)

var MetricsWriteDefault = env.IsDevelopment()

// Flag for enabling writing metrics for sandbox
// https://app.launchdarkly.com/projects/default/flags/sandbox-metrics-read
const (
	MetricsReadFlagName = "sandbox-metrics-read"
)

var MetricsReadDefault = env.IsDevelopment()

// Flag for setting the maximum number of concurrent uploads to GCloud
// https://app.launchdarkly.com/projects/default/flags/gcloud-concurrent-upload-limit
const (
	GcloudConcurrentUploadLimit = "gcloud-concurrent-upload-limit"
)

// 8 concurrent uploads
const GcloudConcurrentUploadLimitDefault = 8

// Flag for setting the maximum number of CPUs for GCloud uploads
// https://app.launchdarkly.com/projects/default/flags/gcloud-max-cpu-quota
const (
	GcloudMaxCPUQuota = "gcloud-max-cpu-quota"
)

// Default is 2% of total CPU
var GcloudMaxCPUQuotaDefault = runtime.NumCPU() * 2 / 100

// Flag for setting the maximum memory limit for GCloud uploads
// https://app.launchdarkly.com/projects/default/flags/gcloud-max-memory-limit
const (
	GcloudMaxMemoryLimit = "gcloud-max-memory-limit"
)

// Default is 0.5% of total memory
var GcloudMaxMemoryLimitDefault = getDefaultMemoryLimit()

// getDefaultMemoryLimit returns the default memory limit for GCloud uploads in MiB
func getDefaultMemoryLimit() int {
	vmStat, err := mem.VirtualMemory()
	if err != nil {
		panic(err)
	}

	totalMemory := vmStat.Total
	// Calculate the memory limit based on the percentage
	return int(0.005 * float64(totalMemory/1024/1024)) // Convert to MiB
}

// Flag for setting the maximum concurrent tasks for GCloud uploads
// https://app.launchdarkly.com/projects/default/flags/gcloud-max-tasks
const (
	GcloudMaxTasks = "gcloud-max-tasks"
	// 16 tasks max
	GcloudMaxTasksDefault = 16
)
