package sandbox

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"syscall"
	"time"

	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
	"golang.org/x/mod/semver"
)

const (
	healthCheckInterval      = 10 * time.Second
	metricsCheckInterval     = 5 * time.Second
	minEnvdVersionForMetrics = "0.1.3"
)

var sigsToListen = []os.Signal{
	syscall.SIGKILL,   // Process termination
	syscall.SIGTERM,   // Termination signal
	syscall.SIGINT,    // Interrupt signal
	syscall.SIGSEGV,   // Segmentation fault
	syscall.SIGBUS,    // Bus error
	syscall.SIGABRT,   // Abnormal termination
	syscall.SIGWINCH,  // Window size change
	syscall.SIGCHLD,   // Child process terminated
	syscall.SIGQUIT,   // Quit signal
	syscall.SIGURG,    // Urgent condition
	syscall.SIGXCPU,   // CPU time limit exceeded
	syscall.SIGXFSZ,   // File size limit exceeded
	syscall.SIGVTALRM, // Virtual timer expired
	syscall.SIGPROF,   // Profiling timer expired
	syscall.SIGUSR1,   // User-defined signal 1
	syscall.SIGUSR2,   // User-defined signal 2
	syscall.SIGSYS,    // Bad system call
}

func (s *Sandbox) logHeathAndUsage(ctx *utils.LockableCancelableContext) {
	healthTicker := time.NewTicker(healthCheckInterval)
	metricsTicker := time.NewTicker(metricsCheckInterval)
	defer func() {
		healthTicker.Stop()
		metricsTicker.Stop()
	}()

	// Get metrics and listen for signals on sandbox startup
	go func() {
		s.LogMetrics(ctx)
		s.LogSignals(ctx)
	}()

	for {
		select {
		case <-healthTicker.C:
			childCtx, cancel := context.WithTimeout(ctx, time.Second)

			ctx.Lock()
			s.Healthcheck(childCtx, false)
			ctx.Unlock()

			cancel()
		case <-metricsTicker.C:
			s.LogMetrics(ctx)
		case <-ctx.Done():
			return
		}
	}
}

func (s *Sandbox) Healthcheck(ctx context.Context, alwaysReport bool) {
	var err error
	defer func() {
		s.Logger.Healthcheck(err == nil, alwaysReport)
	}()

	address := fmt.Sprintf("http://%s:%d/health", s.slot.HostIP(), consts.DefaultEnvdServerPort)

	request, err := http.NewRequestWithContext(ctx, "GET", address, nil)
	if err != nil {
		return
	}

	response, err := httpClient.Do(request)
	if err != nil {
		return
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusNoContent {
		err = fmt.Errorf("unexpected status code: %d", response.StatusCode)
		return
	}

	_, err = io.Copy(io.Discard, response.Body)
	if err != nil {
		return
	}
}

func (s *Sandbox) LogMetrics(ctx context.Context) {
	if isGTEVersion(s.Sandbox.EnvdVersion, minEnvdVersionForMetrics) {
		metrics, err := s.GetMetrics(ctx)
		if err != nil {
			s.Logger.Warnf("failed to get metrics: %s", err)
		} else {
			s.Logger.CPUPct(metrics.CPUPercent)
			s.Logger.MemMiB(metrics.MemTotalMiB, metrics.MemUsedMiB)
		}
	}
}

func (s *Sandbox) LogSignals(ctx context.Context) {
	if isGTEVersion(s.Sandbox.EnvdVersion, minEnvdVersionForMetrics) {
		listenProcessSignals(
			ctx, s.FcPid(), sigsToListen, func(sig os.Signal) { s.Logger.Signal(sig) })
	}
}

func isGTEVersion(curVersion, minVersion string) bool {
	if len(curVersion) > 0 && curVersion[0] != 'v' {
		curVersion = "v" + curVersion
	}

	if !semver.IsValid(curVersion) {
		return false
	}

	return semver.Compare(curVersion, minVersion) >= 0
}
