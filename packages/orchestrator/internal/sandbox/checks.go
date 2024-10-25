package sandbox

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

func (s *Sandbox) logHeathAndUsage(ctx *utils.LockableCancelableContext) {
	for {
		select {
		case <-time.After(10 * time.Second):
			childCtx, cancel := context.WithTimeout(ctx, time.Second)

			ctx.Lock()
			s.Healthcheck(childCtx, false)
			ctx.Unlock()

			cancel()

			stats, err := s.stats.getStats()
			if err != nil {
				s.Logger.Warnf("failed to get stats: %s", err)
			} else {
				s.Logger.CPUUsage(stats.CPUCount)
				s.Logger.MemoryUsage(stats.MemoryMB)
			}
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
