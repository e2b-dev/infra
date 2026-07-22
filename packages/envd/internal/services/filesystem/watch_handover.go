package filesystem

import (
	"context"
	"encoding/json"

	"google.golang.org/protobuf/encoding/protojson"

	rpc "github.com/e2b-dev/infra/packages/envd/internal/services/spec/filesystem"
)

// watcherHandover is the serialized form of an active watcher, carried across an
// envd live-upgrade so the new envd can re-arm it with the same id. Events holds
// protojson-encoded pending FilesystemEvents.
type watcherHandover struct {
	Id               string   `json:"id"`
	Path             string   `json:"path"`
	Recursive        bool     `json:"recursive"`
	IncludeEntryInfo bool     `json:"include_entry_info"`
	Events           [][]byte `json:"events"`
}

// ExportWatchers serializes the active watchers (id, config, and any buffered
// events not yet fetched) into an opaque JSON blob. Called at handover time so
// the process-service handover can embed it. The blob is opaque to the process
// service — this package owns the schema.
func (s Service) ExportWatchers() []byte {
	out := make([]watcherHandover, 0)

	s.watchers.Range(func(id string, fw *FileWatcher) bool {
		fw.Lock.Lock()
		evs := make([][]byte, 0, len(fw.Events))
		for _, e := range fw.Events {
			b, err := protojson.Marshal(e)
			if err != nil {
				// A buffered event that won't marshal is dropped from the handover
				// (the re-armed watcher just won't replay it) — surface it rather
				// than lose it silently.
				s.logger.Warn().Err(err).Str("watcher_id", id).Msg("handover: dropping buffered watcher event that failed to marshal")

				continue
			}
			evs = append(evs, b)
		}
		wh := watcherHandover{
			Id:               id,
			Path:             fw.WatchPath,
			Recursive:        fw.Recursive,
			IncludeEntryInfo: fw.IncludeEntryInfo,
			Events:           evs,
		}
		fw.Lock.Unlock()

		out = append(out, wh)

		return true
	})

	blob, err := json.Marshal(out)
	if err != nil {
		s.logger.Error().Err(err).Msg("handover: marshal watchers failed")

		return nil
	}

	return blob
}

// ImportWatchers re-arms watchers carried across a live-upgrade: it re-creates a
// fresh fsnotify watch for each carried watcher, preserving the original
// watcher id (so GetWatcherEvents keeps working without a client change) and
// re-queuing any pending events. The resume freeze makes this lossless — nothing
// mutates the filesystem during the handover window.
func (s Service) ImportWatchers(blob []byte) (rearmed, failed int) {
	if len(blob) == 0 {
		return 0, 0
	}

	var in []watcherHandover
	if err := json.Unmarshal(blob, &in); err != nil {
		s.logger.Error().Err(err).Msg("handover: unmarshal watchers failed")

		return 0, 0
	}

	for _, wh := range in {
		fw, err := CreateFileWatcher(context.Background(), s.logger, wh.Path, wh.Recursive, wh.IncludeEntryInfo)
		if err != nil {
			failed++
			s.logger.Warn().Err(err).Str("path", wh.Path).Str("watcher_id", wh.Id).Msg("handover: re-arm watcher failed")

			continue
		}
		rearmed++

		var pending []*rpc.FilesystemEvent
		for _, b := range wh.Events {
			e := &rpc.FilesystemEvent{}
			if protojson.Unmarshal(b, e) == nil {
				pending = append(pending, e)
			}
		}
		if len(pending) > 0 {
			fw.Lock.Lock()
			fw.Events = append(pending, fw.Events...)
			fw.Lock.Unlock()
		}

		s.watchers.Store(wh.Id, fw)

		s.logger.Info().
			Str("event_type", "watcher_readopted").
			Str("watcher_id", wh.Id).
			Str("path", wh.Path).
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
