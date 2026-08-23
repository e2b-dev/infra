package filesystem

import (
	"context"

	rpc "github.com/e2b-dev/infra/packages/envd/internal/services/spec/filesystem"
	"github.com/e2b-dev/infra/packages/envd/internal/services/spec/upgrade"
)

// ExportWatchers snapshots the active watchers (id, config, and any buffered
// events not yet fetched) as typed upgrade.HandoverWatcher messages, carried in
// the process-service handover across an envd live-upgrade so the new envd can
// re-arm each watcher with the same id. Pending FilesystemEvents ride natively
// (no protojson round-trip). This package owns the watcher schema; the process
// service only carries the messages.
func (s Service) ExportWatchers() []*upgrade.HandoverWatcher {
	// Snapshot the watcher set under watchersMu so a concurrent CreateWatcher/
	// RemoveWatcher can't make the export an inconsistent view.
	s.watchersMu.Lock()
	defer s.watchersMu.Unlock()

	return s.exportWatchersLocked()
}

// ExportWatchersHold is ExportWatchers but keeps watchersMu held, returning a
// release func the caller invokes to resume serving. The live-upgrade path
// holds it from the snapshot through the execve so a concurrent GetWatcherEvents
// cannot drain a watcher's buffered events in that window — draining there would
// consume events the snapshot already carried, so the re-armed watcher would
// re-deliver them after the swap (double-delivery). GetWatcherEvents takes the
// same lock. A successful execve replaces this process and the held lock
// vanishes with it; on failure the caller's release restores serving.
func (s Service) ExportWatchersHold() ([]*upgrade.HandoverWatcher, func()) {
	s.watchersMu.Lock()

	return s.exportWatchersLocked(), s.watchersMu.Unlock
}

// exportWatchersLocked snapshots the active watchers. The caller must hold
// watchersMu.
func (s Service) exportWatchersLocked() []*upgrade.HandoverWatcher {
	out := make([]*upgrade.HandoverWatcher, 0)

	s.watchers.Range(func(id string, fw *FileWatcher) bool {
		fw.Lock.Lock()
		// Copy the event pointers into an independent slice under fw.Lock so a
		// later append to fw.Events (or the eventual proto.Marshal, which runs
		// outside this lock) sees a stable snapshot.
		pending := make([]*rpc.FilesystemEvent, len(fw.Events))
		copy(pending, fw.Events)
		fw.Lock.Unlock()

		out = append(out, &upgrade.HandoverWatcher{
			Id:               id,
			Path:             fw.WatchPath,
			Recursive:        fw.Recursive,
			IncludeEntryInfo: fw.IncludeEntryInfo,
			PendingEvents:    pending,
		})

		return true
	})

	return out
}

// ImportWatchers re-arms watchers carried across a live-upgrade: it re-creates a
// fresh fsnotify watch for each carried watcher, preserving the original watcher
// id (so GetWatcherEvents keeps working without a client change) and re-queuing
// any pending events. The resume freeze makes this lossless — nothing mutates
// the filesystem during the handover window.
func (s Service) ImportWatchers(watchers []*upgrade.HandoverWatcher) (rearmed, failed int) {
	if len(watchers) == 0 {
		return 0, 0
	}

	// Re-arm under watchersMu so the rebuilt membership is consistent (symmetric
	// with ExportWatchers). No contention in practice — the new envd re-adopts
	// before it serves client RPCs.
	s.watchersMu.Lock()
	defer s.watchersMu.Unlock()

	for _, wh := range watchers {
		fw, err := CreateFileWatcher(context.Background(), s.logger, wh.GetPath(), wh.GetRecursive(), wh.GetIncludeEntryInfo())
		if err != nil {
			failed++
			// Don't log wh.Path — it can be a customer code/data location, and
			// these events go to centralized logs; the opaque watcher_id suffices.
			s.logger.Warn().Err(err).Str("watcher_id", wh.GetId()).Msg("handover: re-arm watcher failed")

			continue
		}
		rearmed++

		pending := wh.GetPendingEvents()
		if len(pending) > 0 {
			fw.Lock.Lock()
			fw.Events = append(pending, fw.Events...)
			fw.Lock.Unlock()
		}

		s.watchers.Store(wh.GetId(), fw)

		s.logger.Info().
			Str("event_type", "watcher_readopted").
			Str("watcher_id", wh.GetId()).
			Int("pending_events", len(pending)).
			Msg("re-armed watcher after envd self-upgrade")
	}

	// Loki-queryable summary (rollout observability).
	s.logger.Info().
		Str("event_type", "watchers_rearmed").
		Int("rearmed", rearmed).
		Int("failed", failed).
		Msg("re-armed filesystem watchers after envd self-upgrade")

	return rearmed, failed
}
