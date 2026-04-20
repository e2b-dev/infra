package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"os/exec"
	"strings"
	"time"

	"github.com/awnumar/memguard"
	"github.com/rs/zerolog"
	"github.com/txn2/txeh"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sys/unix"

	"github.com/e2b-dev/infra/packages/envd/internal/host"
	"github.com/e2b-dev/infra/packages/envd/internal/logs"
	"github.com/e2b-dev/infra/packages/shared/pkg/keys"
)

var (
	ErrAccessTokenMismatch           = errors.New("access token validation failed")
	ErrAccessTokenResetNotAuthorized = errors.New("access token reset not authorized")
)

const (
	maxTimeInPast   = 50 * time.Millisecond
	maxTimeInFuture = 5 * time.Second
)

// validateInitAccessToken validates the access token for /init requests.
// Token is valid if it matches the existing token OR the MMDS hash.
// If neither exists, first-time setup is allowed.
func (a *API) validateInitAccessToken(ctx context.Context, requestToken *SecureToken) error {
	requestTokenSet := requestToken.IsSet()

	// Fast path: token matches existing
	if a.accessToken.IsSet() && requestTokenSet && a.accessToken.EqualsSecure(requestToken) {
		return nil
	}

	// Check MMDS only if token didn't match existing
	matchesMMDS, mmdsExists := a.checkMMDSHash(ctx, requestToken)

	switch {
	case matchesMMDS:
		return nil
	case !a.accessToken.IsSet() && !mmdsExists:
		return nil // first-time setup
	case !requestTokenSet:
		return ErrAccessTokenResetNotAuthorized
	default:
		return ErrAccessTokenMismatch
	}
}

// checkMMDSHash checks if the request token matches the MMDS hash.
// Returns (matches, mmdsExists).
//
// The MMDS hash is set by the orchestrator during Resume:
//   - hash(token): requires this specific token
//   - hash(""): explicitly allows nil token (token reset authorized)
//   - "": MMDS not properly configured, no authorization granted
func (a *API) checkMMDSHash(ctx context.Context, requestToken *SecureToken) (bool, bool) {
	if a.isNotFC {
		return false, false
	}

	mmdsHash, err := a.mmdsClient.GetAccessTokenHash(ctx)
	if err != nil {
		return false, false
	}

	if mmdsHash == "" {
		return false, false
	}

	if !requestToken.IsSet() {
		return mmdsHash == keys.HashAccessToken(""), true
	}

	tokenBytes, err := requestToken.Bytes()
	if err != nil {
		return false, true
	}
	defer memguard.WipeBytes(tokenBytes)

	return keys.HashAccessTokenBytes(tokenBytes) == mmdsHash, true
}

func (a *API) PostInit(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()

	ctx := r.Context()

	operationID := logs.AssignOperationID()
	logger := a.logger.With().Str(string(logs.OperationIDKey), operationID).Logger()

	if r.Body != nil {
		// Read raw body so we can wipe it after parsing
		body, err := io.ReadAll(r.Body)
		// Ensure body is wiped after we're done
		defer memguard.WipeBytes(body)
		if err != nil {
			logger.Error().Msgf("Failed to read request body: %v", err)
			w.WriteHeader(http.StatusBadRequest)

			return
		}

		var initRequest PostInitJSONBody
		if len(body) > 0 {
			err = json.Unmarshal(body, &initRequest)
			if err != nil {
				logger.Error().Msgf("Failed to decode request: %v", err)
				w.WriteHeader(http.StatusBadRequest)

				return
			}
		}

		// Ensure request token is destroyed if not transferred via TakeFrom.
		// This handles: validation failures, timestamp-based skips, and any early returns.
		// Safe because Destroy() is nil-safe and TakeFrom clears the source.
		defer initRequest.AccessToken.Destroy()

		a.initLock.Lock()
		defer a.initLock.Unlock()

		// Update data only if the request is newer or if there's no timestamp at all
		if initRequest.Timestamp == nil || a.lastSetTime.SetToGreater(initRequest.Timestamp.UnixNano()) {
			err = a.SetData(ctx, logger, initRequest)
			if err != nil {
				switch {
				case errors.Is(err, ErrAccessTokenMismatch), errors.Is(err, ErrAccessTokenResetNotAuthorized):
					w.WriteHeader(http.StatusUnauthorized)
				default:
					logger.Error().Msgf("Failed to set data: %v", err)
					w.WriteHeader(http.StatusBadRequest)
				}
				w.Write([]byte(err.Error()))

				return
			}
		}
	}

	go func() { //nolint:contextcheck // TODO: fix this later
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		host.PollForMMDSOpts(ctx, a.mmdsChan, a.defaults.EnvVars)
	}()

	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "")

	w.WriteHeader(http.StatusNoContent)
}

func (a *API) SetData(ctx context.Context, logger zerolog.Logger, data PostInitJSONBody) error {
	// Validate access token before proceeding with any action
	// The request must provide a token that is either:
	// 1. Matches the existing access token (if set), OR
	// 2. Matches the MMDS hash (for token change during resume)
	if err := a.validateInitAccessToken(ctx, data.AccessToken); err != nil {
		return err
	}

	if data.Timestamp != nil {
		// Check if current time differs significantly from the received timestamp
		if shouldSetSystemTime(time.Now(), *data.Timestamp) {
			logger.Debug().Msgf("Setting sandbox start time to: %v", *data.Timestamp)
			ts := unix.NsecToTimespec(data.Timestamp.UnixNano())
			err := unix.ClockSettime(unix.CLOCK_REALTIME, &ts)
			if err != nil {
				logger.Error().Msgf("Failed to set system time: %v", err)
			}
		} else {
			logger.Debug().Msgf("Current time is within acceptable range of timestamp %v, not setting system time", *data.Timestamp)
		}
	}

	if data.EnvVars != nil {
		logger.Debug().Msg(fmt.Sprintf("Setting %d env vars", len(*data.EnvVars)))

		for key, value := range *data.EnvVars {
			logger.Debug().Msgf("Setting env var for %s", key)
			a.defaults.EnvVars.Store(key, value)
		}
	}

	if data.AccessToken.IsSet() {
		logger.Debug().Msg("Setting access token")
		a.accessToken.TakeFrom(data.AccessToken)
	} else if a.accessToken.IsSet() {
		logger.Debug().Msg("Clearing access token")
		a.accessToken.Destroy()
	}

	if data.HyperloopIP != nil {
		go a.SetupHyperloop(*data.HyperloopIP)
	}

	if data.DefaultUser != nil && *data.DefaultUser != "" {
		logger.Debug().Msgf("Setting default user to: %s", *data.DefaultUser)
		a.defaults.User = *data.DefaultUser
	}

	if data.DefaultWorkdir != nil && *data.DefaultWorkdir != "" {
		logger.Debug().Msgf("Setting default workdir to: %s", *data.DefaultWorkdir)
		a.defaults.Workdir = data.DefaultWorkdir
	}

	if data.CaBundle != nil && *data.CaBundle != "" {
		err := a.caCertInstaller.Install(context.WithoutCancel(ctx), *data.CaBundle)
		if err != nil {
			return fmt.Errorf("failed to install CA bundle: %w", err)
		}
	}

	if data.VolumeMounts != nil {
		if err := a.setupNFS(ctx, logger, data.LifecycleID, *data.VolumeMounts); err != nil {
			return fmt.Errorf("failed to setup NFS volumes: %w", err)
		}
	}

	return nil
}

var nfsOptions = strings.Join([]string{
	// wait for data to be sent to proxy server before returning.
	// async might cause issues if the sandbox is shut down suddenly.
	"sync",

	"rsize=1048576",  // 1 MB read buffer
	"wsize=1048576",  // 1 MB write buffer
	"mountproto=tcp", // nfs proxy only supports tcp
	"mountport=2049", // nfs proxy only supports mounting on port 2049
	"proto=tcp",      // nfs proxy only supports tcp
	"port=2049",      // nfs proxy only supports mounting on port 2049
	"nfsvers=3",      // nfs proxy is nfs version 3
	"noacl",          // no reason for acl in the sandbox

	// disable caching so that pause/resume works correctly
	"noac",
	"lookupcache=none",
}, ",")

const nfsMountTimeout = 10 * time.Second

func (a *API) setupNFS(ctx context.Context, logger zerolog.Logger, lifecycleID *string, mounts []VolumeMount) (e error) {
	// Prevent concurrent mounting attempts
	if !a.isMountingNFS.CompareAndSwap(false, true) {
		logger.Debug().Msg("NFS volumes already mounting")

		return e
	}
	defer a.isMountingNFS.Store(false)

	logger.Debug().Msg("Setting up NFS volumes")

	ctx = context.WithoutCancel(ctx)                         // don't allow request context cancellation to propagate
	ctx, cancel := context.WithTimeout(ctx, nfsMountTimeout) // don't let the nfs mount run forever
	defer cancel()

	wg, wgCtx := errgroup.WithContext(ctx)

	requestLifecycleID := derefString(lifecycleID)

	for _, volume := range mounts {
		// Check if this path is already mounted for the current lifecycle
		mountedLifecycle, isMounted := a.mountedPaths.Load(volume.Path)
		mountedLifecycleID := asString(mountedLifecycle)
		if !shouldRemountNFS(isMounted, mountedLifecycleID, requestLifecycleID) {
			logger.Debug().Msgf("Skipping %q, already mounted for lifecycle %q", volume.Path, requestLifecycleID)

			continue
		}

		if isMounted {
			logger.Debug().Msgf("Lifecycle changed for %q: %q -> %q", volume.Path, mountedLifecycleID, requestLifecycleID)
		}

		logger.Debug().Msgf("Setting up %s at %q", volume.NfsTarget, volume.Path)

		wg.Go(func() error {
			// Unmount if currently mounted (handles stale mounts from previous lifecycle)
			if err := a.unmountNFS(wgCtx, logger, volume.Path); err != nil {
				return fmt.Errorf("failed to unmount stale NFS mount at %q: %w", volume.Path, err)
			}

			if err := a.mountNFS(wgCtx, volume.NfsTarget, volume.Path); err != nil {
				return fmt.Errorf("failed to mount NFS at %q: %w", volume.Path, err)
			}

			a.mountedPaths.Store(volume.Path, requestLifecycleID)

			return nil
		})
	}

	return wg.Wait()
}

func (a *API) unmountNFS(ctx context.Context, logger zerolog.Logger, path string) error {
	// Check if actually mounted before trying to unmount.
	// findmnt returns exit code 1 when path is not a mount point - that's not an error.
	data, err := exec.CommandContext(ctx, "findmnt", "--noheadings", "--output", "SOURCE", path).CombinedOutput()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			// Not mounted - nothing to unmount
			return nil
		}

		return fmt.Errorf("failed to check if %q is mounted: %w", path, err)
	}

	source := strings.TrimSpace(string(data))
	if source == "" {
		return nil // already unmounted
	}

	logger.Debug().Msgf("Unmounting stale NFS mount at %q (was: %s)", path, source)

	// Force unmount since the handles are stale anyway
	data, err = exec.CommandContext(ctx, "umount", "--force", path).CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to unmount stale NFS mount at %q: %w\n%s", path, err, string(data))
	}

	// Clear our tracking state for this path
	a.mountedPaths.Delete(path)

	return nil
}

func (a *API) mountNFS(ctx context.Context, nfsTarget, path string) error {
	commands := [][]string{
		{"mkdir", "-p", path},
		{"mount", "-v", "-t", "nfs", "-o", "fg,hard," + nfsOptions, nfsTarget, path},
	}

	for _, command := range commands {
		data, err := exec.CommandContext(ctx, command[0], command[1:]...).CombinedOutput()
		if err != nil {
			return fmt.Errorf("`%s` failed: %w\n%s", strings.Join(command, " "), err, string(data))
		}
	}

	return nil
}

// shouldRemountNFS determines if an NFS volume should be remounted based on lifecycle IDs.
// Returns true if remount is needed, false if we should skip (already mounted for this lifecycle).
//
// Truth table (treating nil/empty as equivalent):
//   - mounted="" + request="" → false (no remount - would cause infinite loop)
//   - mounted="abc" + request="" → true (remount - lifecycle cleared)
//   - mounted="" + request="abc" → true (remount - new lifecycle)
//   - mounted="abc" + request="abc" → false (no remount - same lifecycle)
//   - mounted="abc" + request="xyz" → true (remount - different lifecycle)
func shouldRemountNFS(isMounted bool, mountedLifecycleID, requestLifecycleID string) bool {
	if !isMounted {
		return true // not mounted yet, need to mount
	}

	return mountedLifecycleID != requestLifecycleID
}

func derefString(p *string) string {
	if p == nil {
		return ""
	}

	return *p
}

func asString(v any) string {
	if v == nil {
		return ""
	}

	s, _ := v.(string)

	return s
}

func (a *API) SetupHyperloop(address string) {
	a.hyperloopLock.Lock()
	defer a.hyperloopLock.Unlock()

	if err := rewriteHostsFile(address, "/etc/hosts"); err != nil {
		a.logger.Error().Err(err).Msg("failed to modify hosts file")
	} else {
		a.defaults.EnvVars.Store("E2B_EVENTS_ADDRESS", fmt.Sprintf("http://%s", address))
	}
}

const eventsHost = "events.e2b.local"

func rewriteHostsFile(address, path string) error {
	hosts, err := txeh.NewHosts(&txeh.HostsConfig{
		ReadFilePath:  path,
		WriteFilePath: path,
	})
	if err != nil {
		return fmt.Errorf("failed to create hosts: %w", err)
	}

	// Update /etc/hosts to point events.e2b.local to the hyperloop IP
	// This will remove any existing entries for events.e2b.local first
	ipFamily, err := getIPFamily(address)
	if err != nil {
		return fmt.Errorf("failed to get ip family: %w", err)
	}

	if ok, current, _ := hosts.HostAddressLookup(eventsHost, ipFamily); ok && current == address {
		return nil // nothing to be done
	}

	hosts.AddHost(address, eventsHost)

	return hosts.Save()
}

var (
	ErrInvalidAddress       = errors.New("invalid IP address")
	ErrUnknownAddressFormat = errors.New("unknown IP address format")
)

func getIPFamily(address string) (txeh.IPFamily, error) {
	addressIP, err := netip.ParseAddr(address)
	if err != nil {
		return txeh.IPFamilyV4, fmt.Errorf("failed to parse IP address: %w", err)
	}

	switch {
	case addressIP.Is4():
		return txeh.IPFamilyV4, nil
	case addressIP.Is6():
		return txeh.IPFamilyV6, nil
	default:
		return txeh.IPFamilyV4, fmt.Errorf("%w: %s", ErrUnknownAddressFormat, address)
	}
}

// shouldSetSystemTime returns true if the current time differs significantly from the received timestamp,
// indicating the system clock should be adjusted. Returns true when the sandboxTime is more than
// maxTimeInPast before the hostTime or more than maxTimeInFuture after the hostTime.
func shouldSetSystemTime(sandboxTime, hostTime time.Time) bool {
	return sandboxTime.Before(hostTime.Add(-maxTimeInPast)) || sandboxTime.After(hostTime.Add(maxTimeInFuture))
}
