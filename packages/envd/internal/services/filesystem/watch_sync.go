package filesystem

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"connectrpc.com/connect"
	"github.com/e2b-dev/fsnotify"
	"github.com/rs/zerolog"

	"github.com/e2b-dev/infra/packages/envd/internal/logs"
	"github.com/e2b-dev/infra/packages/envd/internal/permissions"
	rpc "github.com/e2b-dev/infra/packages/envd/internal/services/spec/filesystem"
	"github.com/e2b-dev/infra/packages/envd/internal/utils"
	"github.com/e2b-dev/infra/packages/shared/pkg/id"
)

type FileWatcher struct {
	watcher *fsnotify.Watcher
	Events  []*rpc.FilesystemEvent
	cancel  func()
	Error   error

	Lock sync.Mutex
}

func CreateFileWatcher(ctx context.Context, watchPath string, recursive bool, operationID string, logger *zerolog.Logger) (*FileWatcher, error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("error creating watcher: %w", err))
	}

	// We don't want to cancel the context when the request is finished
	ctx, cancel := context.WithCancel(context.WithoutCancel(ctx))

	err = w.Add(utils.FsnotifyPath(watchPath, recursive))
	if err != nil {
		_ = w.Close()
		cancel()

		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("error adding path %s to watcher: %w", watchPath, err))
	}
	fw := &FileWatcher{
		watcher: w,
		cancel:  cancel,
		Events:  []*rpc.FilesystemEvent{},
		Error:   nil,
	}

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case chErr, ok := <-w.Errors:
				if !ok {
					fw.Error = connect.NewError(connect.CodeInternal, fmt.Errorf("watcher error channel closed"))

					return
				}

				fw.Error = connect.NewError(connect.CodeInternal, fmt.Errorf("watcher error: %w", chErr))

				return
			case e, ok := <-w.Events:
				if !ok {
					fw.Error = connect.NewError(connect.CodeInternal, fmt.Errorf("watcher event channel closed"))

					return
				}

				// One event can have multiple operations.
				ops := []rpc.EventType{}

				if fsnotify.Create.Has(e.Op) {
					ops = append(ops, rpc.EventType_EVENT_TYPE_CREATE)
				}

				if fsnotify.Rename.Has(e.Op) {
					ops = append(ops, rpc.EventType_EVENT_TYPE_RENAME)
				}

				if fsnotify.Chmod.Has(e.Op) {
					ops = append(ops, rpc.EventType_EVENT_TYPE_CHMOD)
				}

				if fsnotify.Write.Has(e.Op) {
					ops = append(ops, rpc.EventType_EVENT_TYPE_WRITE)
				}

				if fsnotify.Remove.Has(e.Op) {
					ops = append(ops, rpc.EventType_EVENT_TYPE_REMOVE)
				}

				for _, op := range ops {
					name, nameErr := filepath.Rel(watchPath, e.Name)
					if nameErr != nil {
						fw.Error = connect.NewError(connect.CodeInternal, fmt.Errorf("error getting relative path: %w", nameErr))

						return
					}

					fw.Lock.Lock()
					fw.Events = append(fw.Events, &rpc.FilesystemEvent{
						Name: name,
						Type: op,
					})
					fw.Lock.Unlock()

					// these are only used for logging
					filesystemEvent := &rpc.WatchDirResponse_Filesystem{
						Filesystem: &rpc.FilesystemEvent{
							Name: name,
							Type: op,
						},
					}
					event := &rpc.WatchDirResponse{
						Event: filesystemEvent,
					}

					logger.
						Debug().
						Str("event_type", "filesystem_event").
						Str(string(logs.OperationIDKey), operationID).
						Interface("filesystem_event", event).
						Msg("Streaming filesystem event")
				}
			}
		}
	}()

	return fw, nil
}

func (fw *FileWatcher) Close() {
	_ = fw.watcher.Close()
	fw.cancel()
}

func (s Service) CreateWatcher(ctx context.Context, req *connect.Request[rpc.CreateWatcherRequest]) (*connect.Response[rpc.CreateWatcherResponse], error) {
	u, err := permissions.GetAuthUser(ctx, s.defaults.User)
	if err != nil {
		return nil, err
	}

	watchPath, err := permissions.ExpandAndResolve(req.Msg.GetPath(), u, s.defaults.Workdir)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	info, err := os.Stat(watchPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("path %s not found: %w", watchPath, err))
		}

		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("error statting path %s: %w", watchPath, err))
	}

	if !info.IsDir() {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("path %s not a directory: %w", watchPath, err))
	}

	// Check if path is on a network filesystem mount
	isNetworkMount, err := IsPathOnNetworkMount(watchPath)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("error checking mount status: %w", err))
	}
	if isNetworkMount {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("cannot watch path on network filesystem: %s", watchPath))
	}

	watcherId := "w" + id.Generate()

	w, err := CreateFileWatcher(ctx, watchPath, req.Msg.GetRecursive(), watcherId, s.logger)
	if err != nil {
		return nil, err
	}

	s.watchers.Store(watcherId, w)

	return connect.NewResponse(&rpc.CreateWatcherResponse{
		WatcherId: watcherId,
	}), nil
}

func (s Service) GetWatcherEvents(_ context.Context, req *connect.Request[rpc.GetWatcherEventsRequest]) (*connect.Response[rpc.GetWatcherEventsResponse], error) {
	watcherId := req.Msg.GetWatcherId()

	w, ok := s.watchers.Load(watcherId)
	if !ok {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("watcher with id %s not found", watcherId))
	}

	if w.Error != nil {
		return nil, w.Error
	}

	w.Lock.Lock()
	defer w.Lock.Unlock()
	events := w.Events
	w.Events = []*rpc.FilesystemEvent{}

	return connect.NewResponse(&rpc.GetWatcherEventsResponse{
		Events: events,
	}), nil
}

func (s Service) RemoveWatcher(_ context.Context, req *connect.Request[rpc.RemoveWatcherRequest]) (*connect.Response[rpc.RemoveWatcherResponse], error) {
	watcherId := req.Msg.GetWatcherId()

	w, ok := s.watchers.Load(watcherId)
	if !ok {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("watcher with id %s not found", watcherId))
	}

	w.Close()
	s.watchers.Delete(watcherId)

	return connect.NewResponse(&rpc.RemoveWatcherResponse{}), nil
}
