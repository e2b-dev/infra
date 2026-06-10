package cleaner

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"
)

func (c *Cleaner) Deleter(ctx context.Context, toDelete <-chan *Candidate, done *sync.WaitGroup) {
	defer done.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case d := <-toDelete:
			if !d.enqueuedAt.IsZero() {
				c.metrics.DeleteQueueWait.Record(ctx, time.Since(d.enqueuedAt).Milliseconds())
			}
			c.deleteFile(ctx, d)
		}
	}
}

var (
	statStandaloneAttrs = metric.WithAttributes(attribute.String(AttrSource, ValSrcAlone))
	unlinkOkAttrs       = metric.WithAttributes(attribute.String(AttrResult, ValResultOk))
	unlinkErrAttrs      = metric.WithAttributes(attribute.String(AttrResult, ValResultErr))
	unlinkAlreadyGone   = metric.WithAttributes(attribute.String(AttrResult, ValResultAGN))
	unlinkSkipAtimeAttr = metric.WithAttributes(attribute.String(AttrResult, ValResultSAC))
)

func (c *Cleaner) deleteFile(ctx context.Context, candidate *Candidate) {
	// Best-effort: get current metadata to detect atime changes or if file is gone.
	// This stat is "standalone" (not fd-relative) — split out from in-dir stats
	// so dashboards can compare LOOKUP-heavy vs GETATTR-only call patterns.
	statStart := time.Now()
	meta, err := c.stat(candidate.FullPath)
	c.metrics.StatDuration.Record(ctx, time.Since(statStart).Milliseconds(), statStandaloneAttrs)
	c.metrics.StatOps.Add(ctx, 1, statStandaloneAttrs)
	c.DeleteAttemptC.Add(1)

	switch {
	case err != nil:
		// NB: existing code's already-gone / err branches are inverted relative
		// to the IsNotExist check; preserved here as-is and the OTEL counter
		// records the true outcome. The counter is the source of truth for new
		// dashboards; the legacy atomic counters retain their old semantics.
		if !os.IsNotExist(err) {
			c.Info(ctx, "error stating file before delete", zap.Error(err))
			c.DeleteAlreadyGoneC.Add(1)
			c.metrics.UnlinkOps.Add(ctx, 1, unlinkErrAttrs)
		} else {
			c.DeleteErrC.Add(1)
			c.metrics.UnlinkOps.Add(ctx, 1, unlinkAlreadyGone)
		}

	case meta.ATimeUnix == candidate.ATimeUnix:
		c.RemoveC.Add(1)
		if !c.DryRun {
			unlinkStart := time.Now()
			err = os.Remove(candidate.FullPath)
			c.metrics.DeleteUnlinkDuration.Record(ctx, time.Since(unlinkStart).Milliseconds())
			c.Debug(ctx, fmt.Sprintf("delete file aged %v: %s",
				time.Since(time.Unix(candidate.ATimeUnix, 0)), candidate.FullPath),
				zap.Duration("duration", time.Since(unlinkStart)))
		}
		if err == nil {
			c.metrics.UnlinkOps.Add(ctx, 1, unlinkOkAttrs)
			c.DeletedBytes.Add(candidate.Size)
			c.root.mu.Lock()
			c.DeletedAge = append(c.DeletedAge, time.Since(time.Unix(candidate.ATimeUnix, 0)))
			c.root.mu.Unlock()
		} else {
			c.metrics.UnlinkOps.Add(ctx, 1, unlinkErrAttrs)
		}

	default:
		c.DeleteSkipC.Add(1)
		c.metrics.UnlinkOps.Add(ctx, 1, unlinkSkipAtimeAttr)
	}
}
