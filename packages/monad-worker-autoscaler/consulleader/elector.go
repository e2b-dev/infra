package consulleader

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type Elector struct {
	Address    string
	Token      string
	LockKey    string
	InstanceID string
	TTL        time.Duration
	Client     *http.Client

	mu          sync.Mutex
	sessionID   string
	lastRenewed time.Time
}

func (e *Elector) Observe(ctx context.Context) (bool, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.Client == nil {
		return false, errors.New("consul HTTP client is required")
	}
	if e.sessionID == "" {
		if err := e.createSession(ctx); err != nil {
			return false, err
		}
	} else if time.Since(e.lastRenewed) >= e.TTL/3 {
		if err := e.renewSession(ctx); err != nil {
			e.sessionID = ""

			return false, err
		}
	}

	endpoint, err := e.endpoint("/v1/kv/" + strings.TrimLeft(e.LockKey, "/"))
	if err != nil {
		return false, err
	}
	query := endpoint.Query()
	query.Set("acquire", e.sessionID)
	endpoint.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, endpoint.String(), strings.NewReader(e.InstanceID))
	if err != nil {
		return false, fmt.Errorf("create Consul lock request: %w", err)
	}
	e.authorize(req)
	resp, err := e.Client.Do(req)
	if err != nil {
		return false, fmt.Errorf("acquire Consul leader lock: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))

		return false, fmt.Errorf("acquire Consul leader lock: unexpected HTTP %d", resp.StatusCode)
	}
	var acquired bool
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1024)).Decode(&acquired); err != nil {
		return false, fmt.Errorf("decode Consul lock response: %w", err)
	}

	return acquired, nil
}

func (e *Elector) Close(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.sessionID == "" || e.Client == nil {
		return nil
	}
	endpoint, err := e.endpoint("/v1/session/destroy/" + url.PathEscape(e.sessionID))
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, endpoint.String(), nil)
	if err != nil {
		return fmt.Errorf("create Consul session destroy request: %w", err)
	}
	e.authorize(req)
	resp, err := e.Client.Do(req)
	if err != nil {
		return fmt.Errorf("destroy Consul leader session: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("destroy Consul leader session: unexpected HTTP %d", resp.StatusCode)
	}
	e.sessionID = ""

	return nil
}

func (e *Elector) createSession(ctx context.Context) error {
	endpoint, err := e.endpoint("/v1/session/create")
	if err != nil {
		return err
	}
	payload, err := json.Marshal(map[string]any{
		"Name":      "monad-worker-autoscaler-" + e.InstanceID,
		"TTL":       e.TTL.String(),
		"Behavior":  "delete",
		"LockDelay": "0s",
	})
	if err != nil {
		return fmt.Errorf("encode Consul session request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, endpoint.String(), bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("create Consul session request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	e.authorize(req)
	resp, err := e.Client.Do(req)
	if err != nil {
		return fmt.Errorf("create Consul leader session: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))

		return fmt.Errorf("create Consul leader session: unexpected HTTP %d", resp.StatusCode)
	}
	var result struct {
		ID string `json:"ID"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&result); err != nil {
		return fmt.Errorf("decode Consul session response: %w", err)
	}
	if result.ID == "" {
		return errors.New("consul returned an empty session ID")
	}
	e.sessionID = result.ID
	e.lastRenewed = time.Now()

	return nil
}

func (e *Elector) renewSession(ctx context.Context) error {
	endpoint, err := e.endpoint("/v1/session/renew/" + url.PathEscape(e.sessionID))
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, endpoint.String(), nil)
	if err != nil {
		return fmt.Errorf("create Consul session renewal request: %w", err)
	}
	e.authorize(req)
	resp, err := e.Client.Do(req)
	if err != nil {
		return fmt.Errorf("renew Consul leader session: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))

		return fmt.Errorf("renew Consul leader session: unexpected HTTP %d", resp.StatusCode)
	}
	e.lastRenewed = time.Now()

	return nil
}

func (e *Elector) endpoint(path string) (*url.URL, error) {
	base, err := url.Parse(e.Address)
	if err != nil {
		return nil, fmt.Errorf("parse Consul address: %w", err)
	}
	base.Path = strings.TrimRight(base.Path, "/") + path
	base.RawQuery = ""

	return base, nil
}

func (e *Elector) authorize(req *http.Request) {
	req.Header.Set("X-Consul-Token", e.Token)
}
