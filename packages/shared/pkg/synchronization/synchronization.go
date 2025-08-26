package synchronization

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

type Store[SourceItem any, PoolItem any] interface {
	SourceList(ctx context.Context) ([]SourceItem, error)
	SourceExists(ctx context.Context, s []SourceItem, p PoolItem) bool

	PoolList(ctx context.Context) []PoolItem
	PoolExists(ctx context.Context, s SourceItem) bool
	PoolInsert(ctx context.Context, s SourceItem)
	PoolUpdate(ctx context.Context, s PoolItem)
	PoolRemove(ctx context.Context, s PoolItem)
}

// Synchronize is a generic type that provides methods for synchronizing a pool of items with a source.
// It uses a Store interface to interact with the source and pool, allowing for flexible synchronization logic.
type Synchronize[SourceItem any, PoolItem any] struct {
	store Store[SourceItem, PoolItem]

	tracer           trace.Tracer
	tracerSpanPrefix string
	logsPrefix       string

	cancel     chan struct{} // channel for cancellation of synchronization
	cancelOnce sync.Once
}

func NewSynchronize[SourceItem any, PoolItem any](tracer trace.Tracer, spanPrefix string, logsPrefix string, store Store[SourceItem, PoolItem]) *Synchronize[SourceItem, PoolItem] {
	s := &Synchronize[SourceItem, PoolItem]{
		tracer:           tracer,
		tracerSpanPrefix: spanPrefix,
		logsPrefix:       logsPrefix,
		store:            store,
		cancel:           make(chan struct{}),
	}

	return s
}

func (s *Synchronize[SourceItem, PoolItem]) Start(ctx context.Context, syncInterval time.Duration, syncRoundTimeout time.Duration, runInitialSync bool) {
	if runInitialSync {
		initialSyncCtx, initialSyncCancel := context.WithTimeout(ctx, syncRoundTimeout)
		err := s.sync(initialSyncCtx)
		initialSyncCancel()
		if err != nil {
			zap.L().Error(s.getLog("Initial sync failed"), zap.Error(err))
		}
	}

	timer := time.NewTicker(syncInterval)
	defer timer.Stop()

	for {
		select {
		case <-s.cancel:
			zap.L().Info(s.getLog("Background synchronization ended"))
			return
		case <-timer.C:
			syncTimeout, syncCancel := context.WithTimeout(ctx, syncRoundTimeout)
			err := s.sync(syncTimeout)
			syncCancel()
			if err != nil {
				zap.L().Error(s.getLog("Failed to synchronize"), zap.Error(err))
			}
		}
	}
}

func (s *Synchronize[SourceItem, PoolItem]) Close() {
	s.cancelOnce.Do(
		func() { close(s.cancel) },
	)
}

func (s *Synchronize[SourceItem, PoolItem]) sync(ctx context.Context) error {
	spanCtx, span := s.tracer.Start(ctx, s.getSpanName("sync-items"))
	defer span.End()

	sourceItems, err := s.store.SourceList(ctx)
	if err != nil {
		return err
	}

	s.syncDiscovered(spanCtx, sourceItems)
	s.syncOutdated(spanCtx, sourceItems)

	return nil
}

func (s *Synchronize[SourceItem, PoolItem]) syncDiscovered(ctx context.Context, sourceItems []SourceItem) {
	spanCtx, span := s.tracer.Start(ctx, s.getSpanName("sync-discovered-items"))
	defer span.End()

	var wg sync.WaitGroup
	defer wg.Wait()

	for _, item := range sourceItems {
		// item already exists in the pool, skip it
		if ok := s.store.PoolExists(ctx, item); ok {
			continue
		}

		// initialize newly discovered item
		wg.Add(1)
		go func(item SourceItem) {
			defer wg.Done()
			s.store.PoolInsert(spanCtx, item)
		}(item)
	}
}

func (s *Synchronize[SourceItem, PoolItem]) syncOutdated(ctx context.Context, sourceItems []SourceItem) {
	spanCtx, span := s.tracer.Start(ctx, s.getSpanName("sync-outdated-items"))
	defer span.End()

	var wg sync.WaitGroup
	defer wg.Wait()

	for _, poolItem := range s.store.PoolList(ctx) {
		found := s.store.SourceExists(ctx, sourceItems, poolItem)
		if found {
			s.store.PoolUpdate(ctx, poolItem)
			continue
		}

		// remove the item that is no longer present in the source
		wg.Add(1)
		go func(poolItem PoolItem) {
			defer wg.Done()
			s.store.PoolRemove(spanCtx, poolItem)
		}(poolItem)
	}
}

func (s *Synchronize[SourceItem, PoolItem]) getSpanName(name string) string {
	return fmt.Sprintf("%s-%s", s.tracerSpanPrefix, name)
}

func (s *Synchronize[SourceItem, PoolItem]) getLog(message string) string {
	return fmt.Sprintf("%s: %s", s.logsPrefix, message)
}
