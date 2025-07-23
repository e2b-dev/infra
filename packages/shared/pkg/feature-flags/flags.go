package feature_flags

import (
	"runtime"

	"github.com/shirou/gopsutil/v4/mem"

	"github.com/e2b-dev/infra/packages/shared/pkg/env"
)

// https://app.launchdarkly.com/projects/default/flags/

type BoolFlag string

const (
	MetricsWriteFlagName BoolFlag = "sandbox-metrics-write"
	MetricsReadFlagName  BoolFlag = "sandbox-metrics-read"
)

type IntFlag string

const (
	// GcloudConcurrentUploadLimit - the maximum number of concurrent uploads to GCloud
	GcloudConcurrentUploadLimit IntFlag = "gcloud-concurrent-upload-limit"
	// GcloudMaxCPUQuota - maximum number of CPUs for GCloud uploads
	GcloudMaxCPUQuota IntFlag = "gcloud-max-cpu-quota"
	// GcloudMaxMemoryLimitMiB - maximum memory limit for GCloud uploads
	GcloudMaxMemoryLimitMiB IntFlag = "gcloud-max-memory-limit"
	// GcloudMaxTasks - maximum concurrent tasks for GCloud uploads
	GcloudMaxTasks IntFlag = "gcloud-max-tasks"
)

var flagsBool = map[BoolFlag]bool{
	MetricsWriteFlagName: metricsWriteDefault,
	MetricsReadFlagName:  metricsReadDefault,
}

var flagsInt = map[IntFlag]int{
	GcloudConcurrentUploadLimit: GcloudConcurrentUploadLimitDefault,
	GcloudMaxCPUQuota:           gcloudMaxCPUQuotaDefault,
	GcloudMaxMemoryLimitMiB:     gcloudMaxMemoryLimitMiBDefault,
	GcloudMaxTasks:              gcloudMaxTasksDefault,
}

const (
	GcloudConcurrentUploadLimitDefault = 8
	gcloudMaxTasksDefault              = 16
)

var (
	metricsWriteDefault = env.IsDevelopment()
	metricsReadDefault  = env.IsDevelopment()
	// gcloudMaxCPUQuotaDefault default is 2% of total CPU (100% is 1 CPU core)
	gcloudMaxCPUQuotaDefault = 2 * runtime.NumCPU()
	// gcloudMaxMemoryLimitMiBDefault default is 0.5% of total memory
	gcloudMaxMemoryLimitMiBDefault = getDefaultMemoryLimitMiB()
)

// getDefaultMemoryLimitMiB returns the default memory limit for GCloud uploads in MiB
func getDefaultMemoryLimitMiB() int {
	vmStat, err := mem.VirtualMemory()
	if err != nil {
		panic(err)
	}

	totalMemory := vmStat.Total
	// Calculate the memory limit based on the percentage
	return int(0.005 * float64(totalMemory) / 1024 / 1024) // Convert to MiB
}
