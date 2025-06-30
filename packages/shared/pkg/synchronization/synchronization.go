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
	Store Store[SourceItem, PoolItem]

	Tracer           trace.Tracer
	TracerSpanPrefix string
	LogsPrefix       string
}

func (s *Synchronize[SourceItem, PoolItem]) Sync(ctx context.Context) error {
	spanCtx, span := s.Tracer.Start(ctx, s.getSpanName("sync-items"))
	defer span.End()

	sourceItems, err := s.Store.SourceList(ctx)
	if err != nil {
		return err
	}

	s.syncDiscovered(spanCtx, sourceItems)
	s.syncOutdated(spanCtx, sourceItems)

	return nil
}

func (s *Synchronize[SourceItem, PoolItem]) StartSync(cancel chan struct{}, syncInterval time.Duration, syncRoundTimeout time.Duration, runInitialSync bool) {
	if runInitialSync {
		initialSyncTimeout, initialSyncCancel := context.WithTimeout(context.Background(), syncRoundTimeout)
		err := s.Sync(initialSyncTimeout)
		initialSyncCancel()
		if err != nil {
			zap.L().Error(s.getLog("Initial sync failed"), zap.Error(err))
		}
	}

	timer := time.NewTicker(syncInterval)
	defer timer.Stop()

	for {
		select {
		case <-cancel:
			zap.L().Info(s.getLog("Background synchronization ended"))
			return
		case <-timer.C:
			syncTimeout, syncCancel := context.WithTimeout(context.Background(), syncRoundTimeout)
			err := s.Sync(syncTimeout)
			syncCancel()
			if err != nil {
				zap.L().Error(s.getLog("Failed to synchronize"), zap.Error(err))
			}
		}
	}
}

func (s *Synchronize[SourceItem, PoolItem]) syncDiscovered(ctx context.Context, sourceItems []SourceItem) {
	spanCtx, span := s.Tracer.Start(ctx, s.getSpanName("sync-discovered-items"))
	defer span.End()

	var wg sync.WaitGroup
	defer wg.Wait()

	for _, item := range sourceItems {
		// item already exists in the pool, skip it
		if ok := s.Store.PoolExists(ctx, item); ok {
			continue
		}

		// initialize newly discovered item
		wg.Add(1)
		go func(item SourceItem) {
			defer wg.Done()
			s.Store.PoolInsert(spanCtx, item)
		}(item)
	}
}

func (s *Synchronize[SourceItem, PoolItem]) syncOutdated(ctx context.Context, sourceItems []SourceItem) {
	spanCtx, span := s.Tracer.Start(ctx, s.getSpanName("sync-outdated-items"))
	defer span.End()

	var wg sync.WaitGroup
	defer wg.Wait()

	for _, poolItem := range s.Store.PoolList(ctx) {
		found := s.Store.SourceExists(ctx, sourceItems, poolItem)
		if found {
			s.Store.PoolUpdate(ctx, poolItem)
			continue
		}

		// remove the item that is no longer present in the source
		wg.Add(1)
		go func(poolItem PoolItem) {
			defer wg.Done()
			s.Store.PoolRemove(spanCtx, poolItem)
		}(poolItem)
	}
}

func (s *Synchronize[SourceItem, PoolItem]) getSpanName(name string) string {
	return fmt.Sprintf("%s-%s", s.TracerSpanPrefix, name)
}

func (s *Synchronize[SourceItem, PoolItem]) getLog(message string) string {
	return fmt.Sprintf("%s: %s", s.LogsPrefix, message)
}
