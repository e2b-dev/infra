package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"os"
	"time"

	"github.com/rs/zerolog"
	"github.com/txn2/txeh"
	"golang.org/x/sys/unix"

	"github.com/e2b-dev/infra/packages/envd/internal/host"
	"github.com/e2b-dev/infra/packages/envd/internal/logs"
	"github.com/e2b-dev/infra/packages/shared/pkg/keys"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

const hostsFilePermissions = 0o644

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
func (a *API) validateInitAccessToken(ctx context.Context, requestToken *string) error {
	// Fast path: token matches existing
	if a.accessToken != nil && requestToken != nil && *requestToken == *a.accessToken {
		return nil
	}

	// Check MMDS only if token didn't match existing
	matchesMMDS, mmdsExists := a.checkMMDSHash(ctx, requestToken)

	switch {
	case matchesMMDS:
		return nil
	case a.accessToken == nil && !mmdsExists:
		return nil // first-time setup
	case requestToken == nil:
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
func (a *API) checkMMDSHash(ctx context.Context, requestToken *string) (bool, bool) {
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

	if requestToken == nil {
		return mmdsHash == keys.HashAccessToken(""), true
	}

	return keys.HashAccessToken(*requestToken) == mmdsHash, true
}

func (a *API) PostInit(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()

	ctx := r.Context()

	operationID := logs.AssignOperationID()
	logger := a.logger.With().Str(string(logs.OperationIDKey), operationID).Logger()

	if r.Body != nil {
		var initRequest PostInitJSONBody

		err := json.NewDecoder(r.Body).Decode(&initRequest)
		if err != nil && !errors.Is(err, io.EOF) {
			logger.Error().Msgf("Failed to decode request: %v", err)
			w.WriteHeader(http.StatusBadRequest)

			return
		}

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

	if data.AccessToken != nil {
		logger.Debug().Msg("Setting access token")
	} else {
		logger.Debug().Msg("Clearing access token")
	}
	a.accessToken = data.AccessToken

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

	return nil
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
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read hosts file: %w", err)
	}

	// the txeh library drops an entry if the file does not end with a newline
	if len(data) > 0 && data[len(data)-1] != '\n' {
		data = append(data, '\n')
	}

	hosts, err := txeh.NewHosts(&txeh.HostsConfig{RawText: utils.ToPtr(string(data))})
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

	if err = os.WriteFile(path, []byte(hosts.RenderHostsFile()), hostsFilePermissions); err != nil {
		return fmt.Errorf("failed to save hosts file: %w", err)
	}

	return nil
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
