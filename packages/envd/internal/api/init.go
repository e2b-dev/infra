package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/rs/zerolog"
	"github.com/txn2/txeh"
	"golang.org/x/sys/unix"

	"github.com/e2b-dev/infra/packages/envd/internal/host"
	"github.com/e2b-dev/infra/packages/envd/internal/logs"
)

var ErrAccessTokenAlreadySet = errors.New("access token is already set")

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
		if initRequest.Timestamp == nil ||
			(initRequest.Timestamp != nil && a.lastSetTime.SetToGreater(initRequest.Timestamp.UnixNano())) {
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
		logger.Debug().Msgf("Setting sandbox start time to: %v", *data.Timestamp)
		ts := unix.NsecToTimespec(data.Timestamp.UnixNano())
		err := unix.ClockSettime(unix.CLOCK_REALTIME, &ts)
		if err != nil {
			logger.Error().Msgf("Failed to set system time: %v", err)
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

	hosts, err := txeh.NewHosts(&txeh.HostsConfig{ReadFilePath: "/etc/hosts", WriteFilePath: "/etc/hosts"})
	if err != nil {
		a.logger.Error().Msgf("Failed to create hosts: %v", err)
		return
	}

	// Update /etc/hosts to point events.e2b.local to the hyperloop IP
	// This will remove any existing entries for events.e2b.local first
	hosts.AddHost(address, "events.e2b.local")
	err = hosts.Save()
	if err != nil {
		a.logger.Error().Msgf("Failed to add events host entry: %v", err)
	}

	a.envVars.Store("E2B_EVENTS_ADDRESS", fmt.Sprintf("http://%s", address))
}
