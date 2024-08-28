package filesystem

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"

	"github.com/e2b-dev/infra/packages/envd/internal/logs"
	"github.com/e2b-dev/infra/packages/envd/internal/permissions"
	rpc "github.com/e2b-dev/infra/packages/envd/internal/services/spec/filesystem"

	"connectrpc.com/connect"
	"github.com/fsnotify/fsnotify"
)

// When performing a recursive directory traversal we want to exclude the following directories from being watched for a few reasons:
// - Performance: Watching the entire filesystem recursively can be extremely resource-intensive
// - Permissions: Even with sudo, some parts of the filesystem might not be accessible due to mount options or other restrictions.
// - Spamming: this could generate a lot of output very quickly.
// - Security implications: Running this command as root could potentially expose sensitive information.
// - Filter out non-regular files such as device and character files ("/dev"), virtual files ("/proc"), sockets, etc.
var doNotWatchDirsRegex = regexp.MustCompile("^/($|proc|sys|dev|usr)")

func (s Service) WatchDir(ctx context.Context, req *connect.Request[rpc.WatchDirRequest], stream *connect.ServerStream[rpc.WatchDirResponse]) error {
	return logs.LogServerStreamWithoutEvents(ctx, s.logger, req, stream, s.watchHandler)
}

func (s Service) watchHandler(ctx context.Context, req *connect.Request[rpc.WatchDirRequest], stream *connect.ServerStream[rpc.WatchDirResponse]) error {
	u, err := permissions.GetAuthUser(ctx)
	if err != nil {
		return err
	}

	watchPath, err := permissions.ExpandAndResolve(req.Msg.GetPath(), u)
	if err != nil {
		return connect.NewError(connect.CodeInvalidArgument, err)
	}

	info, err := os.Lstat(watchPath)
	if err != nil {
		if os.IsNotExist(err) {
			return connect.NewError(connect.CodeNotFound, fmt.Errorf("path %s not found: %w", watchPath, err))
		}

		return connect.NewError(connect.CodeInternal, fmt.Errorf("error statting path %s: %w", watchPath, err))
	}

	if !info.IsDir() {
		return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("path %s not a directory: %w", watchPath, err))
	}

	w, err := fsnotify.NewWatcher()
	if err != nil {
		return connect.NewError(connect.CodeInternal, fmt.Errorf("error creating watcher: %w", err))
	}
	defer w.Close()

	err = w.Add(watchPath)
	if err != nil {
		return connect.NewError(connect.CodeInternal, fmt.Errorf("error adding path %s to watcher: %w", watchPath, err))
	}

	// Perform a recursive directory traversal to watch nested directories
	err = filepath.WalkDir(watchPath, func(path string, d fs.DirEntry, err error) error {
		if doNotWatchDirsRegex.MatchString(path) {
			return nil
		}

		if err != nil {
			return err
		}

		if d.IsDir() {
			return w.Add(path)
		}

		return nil
	})
	if err != nil {
		return connect.NewError(connect.CodeInternal, fmt.Errorf("error adding path %s to watcher: %w", watchPath, err))
	}

	err = stream.Send(&rpc.WatchDirResponse{
		Event: &rpc.WatchDirResponse_Start{
			Start: &rpc.WatchDirResponse_StartEvent{},
		},
	})
	if err != nil {
		return connect.NewError(connect.CodeUnknown, fmt.Errorf("error sending start event: %w", err))
	}

	keepaliveTicker, resetKeepalive := permissions.GetKeepAliveTicker(req)
	defer keepaliveTicker.Stop()

	for {
		select {
		case <-keepaliveTicker.C:
			streamErr := stream.Send(&rpc.WatchDirResponse{
				Event: &rpc.WatchDirResponse_Keepalive{
					Keepalive: &rpc.WatchDirResponse_KeepAlive{},
				},
			})
			if streamErr != nil {
				return connect.NewError(connect.CodeUnknown, streamErr)
			}
		case <-ctx.Done():
			return ctx.Err()
		case chErr, ok := <-w.Errors:
			if !ok {
				return connect.NewError(connect.CodeInternal, fmt.Errorf("watcher error channel closed"))
			}

			return connect.NewError(connect.CodeInternal, fmt.Errorf("watcher error: %w", chErr))
		case e, ok := <-w.Events:
			if !ok {
				return connect.NewError(connect.CodeInternal, fmt.Errorf("watcher event channel closed"))
			}

			// One event can have multiple operations.
			ops := []rpc.EventType{}

			if fsnotify.Create.Has(e.Op) {
				ops = append(ops, rpc.EventType_EVENT_TYPE_CREATE)
				if err := determineWatch(w, e.Name); err != nil {
					return err
				}
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
				path := filepath.Clean(e.Name)

				filesystemEvent := &rpc.WatchDirResponse_Filesystem{
					Filesystem: &rpc.WatchDirResponse_FilesystemEvent{
						Name: path,
						Type: op,
					},
				}

				event := &rpc.WatchDirResponse{
					Event: filesystemEvent,
				}

				streamErr := stream.Send(event)

				s.logger.
					Debug().
					Str("event_type", "filesystem_event").
					Str(string(logs.OperationIDKey), ctx.Value(logs.OperationIDKey).(string)).
					Interface("filesystem_event", event).
					Msg("Streaming filesystem event")

				if streamErr != nil {
					return connect.NewError(connect.CodeUnknown, streamErr)
				}

				resetKeepalive()
			}
		}
	}
}

func determineWatch(w *fsnotify.Watcher, path string) error {
	cleanPath := filepath.Clean(path)

	info, err := os.Lstat(cleanPath)
	if err != nil {
		if os.IsNotExist(err) {
			return connect.NewError(connect.CodeNotFound, fmt.Errorf("path %s not found: %w", cleanPath, err))
		}

		return connect.NewError(connect.CodeInternal, fmt.Errorf("error statting path %s: %w", cleanPath, err))
	}

	// When a new directory is created, add it to the watch list
	if info.IsDir() && !doNotWatchDirsRegex.MatchString(cleanPath) {
		// TODO when /a/b is created, only /a is added to watch list
		err := w.Add(cleanPath)
		if err != nil {
			return connect.NewError(connect.CodeInternal, fmt.Errorf("error adding path %s to watcher: %w", cleanPath, err))
		}
	}

	return nil
}
