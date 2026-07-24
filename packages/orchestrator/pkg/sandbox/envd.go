//go:build linux

package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"syscall"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/envd"
	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

const (
	loopDelay = 5 * time.Millisecond
)

// envdInitExitType classifies the outcome of an envd init call.
type envdInitExitType string

const (
	envdInitExitSuccess  envdInitExitType = "success"
	envdInitExitTimeout  envdInitExitType = "timeout"
	envdInitExitCanceled envdInitExitType = "canceled"
	envdInitExitOther    envdInitExitType = "other"
	// envdInitExitTransient marks a retried attempt that failed but was not the
	// terminal outcome of the init episode.
	envdInitExitTransient envdInitExitType = "transient"
)

// classifyEnvdInitExit maps an init error to an exit_type.
func classifyEnvdInitExit(err error) envdInitExitType {
	switch {
	case err == nil:
		return envdInitExitSuccess
	case errors.Is(err, ErrWaitForEnvdTimeout), errors.Is(err, context.DeadlineExceeded):
		return envdInitExitTimeout
	case errors.Is(err, ErrFcProcessExited):
		return envdInitExitOther
	case errors.Is(err, context.Canceled):
		return envdInitExitCanceled
	default:
		return envdInitExitOther
	}
}

// envdOp is the path segment of a parameterless envd POST endpoint.
type envdOp string

const (
	envdOpFreeze   envdOp = "freeze"
	envdOpUnfreeze envdOp = "unfreeze"
	envdOpFsfreeze envdOp = "fsfreeze"
	envdOpFsthaw   envdOp = "fsthaw"
)

// doRequestWithInfiniteRetries does a request with infinite retries until the context is done.
// The parent context should have a deadline or a timeout.
func (s *Sandbox) doRequestWithInfiniteRetries(
	ctx context.Context,
	method,
	address string,
) (*http.Response, int64, error) {
	requestCount := int64(0)

	jsonBody := &envd.PostInitJSONBody{
		LifecycleID:    s.LifecycleID,
		EnvVars:        s.Config.Envd.Vars,
		HyperloopIP:    s.config.NetworkConfig.OrchestratorInSandboxIPAddress,
		AccessToken:    utils.DerefOrDefault(s.Config.Envd.AccessToken, ""),
		DefaultUser:    utils.DerefOrDefault(s.Config.Envd.DefaultUser, ""),
		DefaultWorkdir: utils.DerefOrDefault(s.Config.Envd.DefaultWorkdir, ""),
		VolumeMounts:   s.convertMounts(s.Config.VolumeMounts),
		CaBundle:       s.CABundle,
	}

	for {
		jsonBody.Timestamp = time.Now()

		body, err := json.Marshal(jsonBody)
		if err != nil {
			return nil, requestCount, err
		}

		requestCount++
		reqCtx, cancel := context.WithTimeout(ctx, s.internalConfig.EnvdInitRequestTimeout)
		request, err := http.NewRequestWithContext(reqCtx, method, address, bytes.NewReader(body))
		if err != nil {
			cancel()

			return nil, requestCount, err
		}

		// make sure request to already authorized envd will not fail
		// this can happen in sandbox resume and in some edge cases when previous request was success, but we continued
		if s.Config.Envd.AccessToken != nil {
			request.Header.Set("X-Access-Token", *s.Config.Envd.AccessToken)
		}

		response, err := sandboxHttpClient.Do(request)
		cancel()

		if err == nil {
			return response, requestCount, nil
		}

		select {
		case <-ctx.Done():
			return nil, requestCount, fmt.Errorf("%w with cause: %w", ctx.Err(), context.Cause(ctx))
		case <-time.After(loopDelay):
		}
	}
}

// callEnvdFreeze calls envd's native POST /freeze endpoint to freeze
// user/pty cgroups directly (no Process.Start, no shell). Used pre-pause
// with a tight, freeze-only timeout.
func (s *Sandbox) callEnvdFreeze(ctx context.Context, timeout time.Duration) error {
	return s.callEnvdPostOp(ctx, timeout, envdOpFreeze)
}

// callEnvdUnfreeze calls envd's native POST /unfreeze endpoint. Reserved for
// the pause-failure rollback path; the resume thaw runs via /init's deferred
// unfreeze and does not use this.
func (s *Sandbox) callEnvdUnfreeze(ctx context.Context, timeout time.Duration) error {
	return s.callEnvdPostOp(ctx, timeout, envdOpUnfreeze)
}

// callEnvdFsfreeze calls envd's native POST /fsfreeze endpoint to freeze the
// guest rootfs before a filesystem-only pause, flushing it to a consistent
// on-disk state.
func (s *Sandbox) callEnvdFsfreeze(ctx context.Context, timeout time.Duration) error {
	return s.callEnvdPostOp(ctx, timeout, envdOpFsfreeze)
}

// callEnvdFsthaw calls envd's native POST /fsthaw endpoint. Reserved for the
// pause-failure rollback path so a frozen rootfs can't leave the live VM
// deadlocked.
func (s *Sandbox) callEnvdFsthaw(ctx context.Context, timeout time.Duration) error {
	return s.callEnvdPostOp(ctx, timeout, envdOpFsthaw)
}

func (s *Sandbox) callEnvdPostOp(ctx context.Context, timeout time.Duration, op envdOp) error {
	return s.postEnvd(ctx, timeout, string(op))
}

// callEnvdCollapse calls envd's native POST /collapse endpoint, which compacts
// envd's own anonymous heap into 2 MiB hugepages before pause so it faults
// fewer distinct frames on resume. Unlike freeze/unfreeze it returns a body:
// the per-call collapse stats, which the caller records as metrics and span
// attributes.
func (s *Sandbox) callEnvdCollapse(ctx context.Context, timeout time.Duration) (envd.CollapseResult, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	resp, err := s.doEnvdPost(ctx, "collapse")
	if err != nil {
		return envd.CollapseResult{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)

		return envd.CollapseResult{}, fmt.Errorf("collapse returned %d: %s", resp.StatusCode, utils.Truncate(string(body), 100))
	}

	var result envd.CollapseResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return envd.CollapseResult{}, fmt.Errorf("decode collapse result: %w", err)
	}

	return result, nil
}

// postEnvd issues an authenticated POST to envd's /<path> endpoint with a tight,
// dedicated deadline and expects 204 No Content.
func (s *Sandbox) postEnvd(ctx context.Context, timeout time.Duration, path string) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	resp, err := s.doEnvdPost(ctx, path)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)

		return fmt.Errorf("%s returned %d: %s", path, resp.StatusCode, utils.Truncate(string(body), 100))
	}

	return nil
}

// CallEnvdUpgrade triggers envd's POST /upgrade — the orchestrator-driven
// live-upgrade trigger. It streams the new envd binary
// from localSrcPath as the (authenticated) request body; envd writes it to
// guestBinPath inside the guest and then same-PID re-execs into it. Delivering
// over the token-authenticated /upgrade endpoint avoids the unauthenticated
// /files path that a runtime (post-/init) sandbox rejects.
//
// envd reads the whole body, then execs and never responds, so the connection
// drops without a reply: a transport error after the body was sent is the
// expected success path. The caller must follow with WaitForEnvd.
func (s *Sandbox) CallEnvdUpgrade(ctx context.Context, localSrcPath, guestBinPath string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	f, err := os.Open(localSrcPath)
	if err != nil {
		return fmt.Errorf("open envd source %s: %w", localSrcPath, err)
	}
	defer f.Close()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.envdServerURL()+"/upgrade", f)
	if err != nil {
		return fmt.Errorf("build upgrade request: %w", err)
	}
	if fi, statErr := f.Stat(); statErr == nil {
		req.ContentLength = fi.Size()
	}
	if s.Config.Envd.AccessToken != nil {
		req.Header.Set("X-Access-Token", *s.Config.Envd.AccessToken)
	}
	req.Header.Set("X-Envd-Upgrade-Bin", guestBinPath)

	resp, err := sandboxHttpClient.Do(req)
	if err != nil {
		// envd reads the whole body, then execs without responding, so a
		// transport error AFTER the request reached it (connection reset/EOF) is
		// the expected success path. But a failure to even reach a running envd
		// (connection refused, or a dial-phase failure) means the upgrade was
		// never delivered — surface it so the caller doesn't record a false
		// success. A deadline is deliberately NOT treated as delivery failure
		// (ambiguous — see isUpgradeDeliveryFailure — left to version confirm).
		if isUpgradeDeliveryFailure(err) {
			return fmt.Errorf("deliver upgrade to envd: %w", err)
		}

		return nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)

		return fmt.Errorf("upgrade returned %d: %s", resp.StatusCode, utils.Truncate(string(body), 100))
	}

	return nil
}

// isUpgradeDeliveryFailure reports whether an error from the /upgrade request
// means the binary never reached a running envd — a genuine failure — as opposed
// to the expected post-send connection drop when envd execs mid-response.
//
// A deadline is deliberately NOT treated as failure: it's ambiguous (envd may
// have exec'd and simply never answered), so it's left to the post-upgrade
// version confirmation to decide the true outcome.
func isUpgradeDeliveryFailure(err error) bool {
	// Covered (=> true, "the binary never reached a running envd, so no swap"):
	//   - syscall.ECONNREFUSED: nothing is listening on the envd port.
	//   - net.OpError with Op == "dial": the connection could not be established
	//     (DNS / dial-phase failure) before any byte was sent.
	// Deliberately NOT covered (=> false, i.e. treated as the expected
	// post-send drop): connection reset / EOF / unexpected EOF after the body
	// was sent (envd exec'd mid-response), and context deadline / timeout — the
	// latter is ambiguous (envd may have exec'd and simply never answered), so
	// it is left to the post-upgrade version confirmation to decide the outcome.
	if errors.Is(err, syscall.ECONNREFUSED) {
		return true
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) && opErr.Op == "dial" {
		return true
	}

	return false
}

// setLiveEnvdVersion records the version the running envd last reported.
func (s *Sandbox) setLiveEnvdVersion(v string) {
	s.liveEnvdVersion.Store(&v)
}

// LiveEnvdVersion returns the version the running envd last reported on /init,
// or "" if none has been captured yet.
func (s *Sandbox) LiveEnvdVersion() string {
	if p := s.liveEnvdVersion.Load(); p != nil {
		return *p
	}

	return ""
}

// EnvdHandoverResult is the live-upgrade handover outcome the running envd
// reported on /init (X-Envd-Handover). Its JSON tags match envd's
// api.handoverResult so the header unmarshals directly.
type EnvdHandoverResult struct {
	// Every item is total-carried + failed-subset (ok = total - failed).
	Procs          int `json:"procs"`
	ProcsFailed    int `json:"procs_failed"`
	Retained       int `json:"retained"`
	RetainedFailed int `json:"retained_failed"`
	Watchers       int `json:"watchers"`
	WatchersFailed int `json:"watchers_failed"`
}

// setHandoverResult records the handover outcome the running envd last reported.
func (s *Sandbox) setHandoverResult(h *EnvdHandoverResult) {
	s.handoverResult.Store(h)
}

// HandoverResult returns the last handover outcome the running envd reported on
// /init, or nil if it never booted from a live-upgrade handover.
func (s *Sandbox) HandoverResult() *EnvdHandoverResult {
	return s.handoverResult.Load()
}

// envdServerURL returns the base URL (scheme://host:port) of the sandbox's envd
// HTTP server. A non-empty internalConfig.envdServerURLOverride redirects it
// (test-only; production always uses the slot IP and the default envd port).
func (s *Sandbox) envdServerURL() string {
	if s.internalConfig.envdServerURLOverride != "" {
		return s.internalConfig.envdServerURLOverride
	}

	return fmt.Sprintf("http://%s:%d", s.Slot.HostIPString(), consts.DefaultEnvdServerPort)
}

// doEnvdPost builds and sends an authenticated POST to envd's /<path> endpoint.
// The caller owns the returned response and must close its body. Status handling
// is left to the caller because the endpoints disagree on success: /collapse
// returns 200 with a body, while the cgroup ops return 204 No Content. The
// deadline must live on ctx (callers set it via context.WithTimeout) so it
// stays in force while the caller reads the body.
func (s *Sandbox) doEnvdPost(ctx context.Context, path string) (*http.Response, error) {
	address := s.envdServerURL() + "/" + path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, address, nil)
	if err != nil {
		return nil, fmt.Errorf("build %s request: %w", path, err)
	}
	if s.Config.Envd.AccessToken != nil {
		req.Header.Set("X-Access-Token", *s.Config.Envd.AccessToken)
	}

	resp, err := sandboxHttpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s request: %w", path, err)
	}

	return resp, nil
}

func (s *Sandbox) convertMounts(mounts []VolumeMountConfig) []envd.VolumeMount {
	results := make([]envd.VolumeMount, 0, len(mounts))

	for _, mount := range mounts {
		results = append(results, envd.VolumeMount{
			NfsTarget: fmt.Sprintf("%s:/%s", s.config.NetworkConfig.OrchestratorInSandboxIPAddress, mount.Name),
			Path:      mount.Path,
		})
	}

	return results
}

func (s *Sandbox) initEnvd(ctx context.Context, startType StartType, recordMetrics bool) (e error) {
	ctx, span := tracer.Start(ctx, "envd-init", trace.WithAttributes(telemetry.WithEnvdVersion(s.Config.Envd.Version)))
	defer func() {
		if e != nil {
			span.SetStatus(codes.Error, e.Error())
		}

		span.End()
	}()

	attributes := []attribute.KeyValue{
		telemetry.WithEnvdVersion(s.Config.Envd.Version),
		attribute.Int64("timeout_ms", s.internalConfig.EnvdInitRequestTimeout.Milliseconds()),
		attribute.String("start_type", string(startType)),
	}

	// success is kept for backward compatibility until consumers move to exit_type.
	callAttributes := func(exit envdInitExitType) []attribute.KeyValue {
		return append(attributes,
			attribute.Bool("success", exit == envdInitExitSuccess),
			attribute.String("exit_type", string(exit)),
		)
	}

	address := fmt.Sprintf("http://%s:%d/init", s.Slot.HostIPString(), consts.DefaultEnvdServerPort)

	response, count, err := s.doRequestWithInfiniteRetries(ctx, http.MethodPost, address)
	if err != nil {
		logger.L().Error(ctx, "failed to init envd after retries",
			logger.WithSandboxID(s.Runtime.SandboxID),
			logger.WithEnvdVersion(s.Config.Envd.Version),
			zap.Int64("timeout_ms", s.internalConfig.EnvdInitRequestTimeout.Milliseconds()),
			zap.Int64("attempts", count),
			zap.Error(err),
		)

		exit := classifyEnvdInitExit(err)
		// Count only on the first WaitForEnvd (the real start); a later re-check
		// on the same handler (post-upgrade readiness, template-build swap) must
		// not double-count the resume KPI.
		if recordMetrics {
			envdInitCalls.Add(ctx, count, metric.WithAttributes(callAttributes(exit)...))
		}

		return fmt.Errorf("failed to init envd: %w", err)
	}

	if recordMetrics && count > 1 {
		// Retried attempts were transient per-request failures that preceded the success.
		envdInitCalls.Add(ctx, count-1, metric.WithAttributes(callAttributes(envdInitExitTransient)...))
	}

	// Track successful envd init (first WaitForEnvd only — see recordMetrics).
	if recordMetrics {
		envdInitCalls.Add(ctx, 1, metric.WithAttributes(callAttributes(envdInitExitSuccess)...))
	}

	defer response.Body.Close()
	// Capture the version the running envd reports (X-Envd-Version). This rides
	// on the /init call the resume path already makes — before and after an
	// upgrade — so the upgrade trigger can decide/label/confirm against the live
	// version with no extra round-trip.
	if v := response.Header.Get("X-Envd-Version"); v != "" {
		s.setLiveEnvdVersion(v)
	}
	// Alongside the version, capture the handover outcome the new envd advertises
	// after a live upgrade (X-Envd-Handover) so the trigger can record it.
	if h := response.Header.Get("X-Envd-Handover"); h != "" {
		var hr EnvdHandoverResult
		if err := json.Unmarshal([]byte(h), &hr); err == nil {
			s.setHandoverResult(&hr)
		}
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return fmt.Errorf("failed to read envd init response body: %w", err)
	}

	if response.StatusCode != http.StatusNoContent {
		logger.L().Error(ctx, "envd init request failed",
			logger.WithSandboxID(s.Runtime.SandboxID),
			logger.WithEnvdVersion(s.Config.Envd.Version),
			zap.Int("status_code", response.StatusCode),
			zap.String("response_body", utils.Truncate(string(body), 100)),
		)

		return fmt.Errorf("unexpected status code: %d", response.StatusCode)
	}

	logger.L().Debug(ctx, "succeeded to init envd",
		logger.WithSandboxID(s.Runtime.SandboxID),
		logger.WithEnvdVersion(s.Config.Envd.Version),
		zap.Int64("timeout_ms", s.internalConfig.EnvdInitRequestTimeout.Milliseconds()),
		zap.Int64("attempts", count),
	)

	span.SetStatus(codes.Ok, fmt.Sprintf("envd init returned %d", response.StatusCode))

	return nil
}
