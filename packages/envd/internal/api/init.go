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
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

const hostsFilePermissions = 0o644

var ErrAccessTokenAlreadySet = errors.New("access token is already set")

const (
	maxTimeInPast   = 50 * time.Millisecond
	maxTimeInFuture = 5 * time.Second
)

func (a *API) PostInit(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()

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
			err = a.SetData(logger, initRequest)
			if err != nil {
				switch {
				case errors.Is(err, ErrAccessTokenAlreadySet):
					w.WriteHeader(http.StatusConflict)
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
		host.PollForMMDSOpts(ctx, a.mmdsChan, a.envVars)
	}()

	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "")

	w.WriteHeader(http.StatusNoContent)
}

func (a *API) SetData(logger zerolog.Logger, data PostInitJSONBody) error {
	if data.Timestamp != nil {
		// Check if the time is before maxTimeInPast or after maxTimeInFuture
		if data.Timestamp.Before(time.Now().Add(-maxTimeInPast)) ||
			data.Timestamp.After(time.Now().Add(maxTimeInFuture)) {
			logger.Debug().Msgf("Setting sandbox start time to: %v", *data.Timestamp)
			ts := unix.NsecToTimespec(data.Timestamp.UnixNano())
			err := unix.ClockSettime(unix.CLOCK_REALTIME, &ts)
			if err != nil {
				logger.Error().Msgf("Failed to set system time: %v", err)
			}
		} else {
			logger.Debug().Msgf("Timestamp %v is not far enough in the past or future, not setting system time", *data.Timestamp)
		}
	}

	if data.EnvVars != nil {
		logger.Debug().Msg(fmt.Sprintf("Setting %d env vars", len(*data.EnvVars)))

		for key, value := range *data.EnvVars {
			logger.Debug().Msgf("Setting env var for %s", key)
			a.envVars.Store(key, value)
		}
	}

	if data.AccessToken != nil {
		if a.accessToken != nil && *data.AccessToken != *a.accessToken {
			logger.Error().Msg("Access token is already set and cannot be changed")
			return ErrAccessTokenAlreadySet
		}

		logger.Debug().Msg("Setting access token")
		a.accessToken = data.AccessToken
	}

	if data.HyperloopIP != nil {
		go a.SetupHyperloop(*data.HyperloopIP)
	}

	return nil
}

func (a *API) SetupHyperloop(address string) {
	a.hyperloopLock.Lock()
	defer a.hyperloopLock.Unlock()

	if err := rewriteHostsFile(address, "/etc/hosts"); err != nil {
		a.logger.Error().Err(err).Msg("failed to modify hosts file")
	} else {
		a.envVars.Store("E2B_EVENTS_ADDRESS", fmt.Sprintf("http://%s", address))
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
