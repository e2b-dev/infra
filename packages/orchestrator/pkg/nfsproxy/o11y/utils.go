package o11y

import (
	"errors"
	"os"
	"time"

	"go.opentelemetry.io/otel/attribute"
)

func isUserError(err error) bool {
	return errors.Is(err, os.ErrNotExist) || errors.Is(err, os.ErrExist)
}

func classifyResult(err error) string {
	if err == nil {
		return "success"
	}

	if isUserError(err) {
		return "client_error"
	}

	return "other_error"
}

func argsToAttrs(op string, args []any) []attribute.KeyValue {
	var attrs []attribute.KeyValue

	switch op {
	case "FS.Create", "FS.Open", "FS.Stat", "FS.Lstat", "FS.Remove", "FS.Readlink":
		if len(args) > 0 {
			if s, ok := args[0].(string); ok {
				attrs = append(attrs, attribute.String("nfs.filename", s))
			}
		}
	case "FS.Rename":
		if len(args) > 0 {
			if s, ok := args[0].(string); ok {
				attrs = append(attrs, attribute.String("nfs.oldpath", s))
			}
		}
		if len(args) > 1 {
			if s, ok := args[1].(string); ok {
				attrs = append(attrs, attribute.String("nfs.newpath", s))
			}
		}
	case "FS.OpenFile":
		if len(args) > 0 {
			if s, ok := args[0].(string); ok {
				attrs = append(attrs, attribute.String("nfs.filename", s))
			}
		}
		if len(args) > 1 {
			if flag, ok := args[1].(int); ok {
				attrs = append(attrs, attribute.Int("nfs.flag", flag))
			}
		}
		if len(args) > 2 {
			if perm, ok := args[2].(os.FileMode); ok {
				attrs = append(attrs, attribute.String("nfs.perm", perm.String()))
			}
		}
	case "FS.TempFile":
		if len(args) > 0 {
			if s, ok := args[0].(string); ok {
				attrs = append(attrs, attribute.String("nfs.dir", s))
			}
		}
		if len(args) > 1 {
			if s, ok := args[1].(string); ok {
				attrs = append(attrs, attribute.String("nfs.prefix", s))
			}
		}
	case "FS.ReadDir", "FS.Chroot":
		if len(args) > 0 {
			if s, ok := args[0].(string); ok {
				attrs = append(attrs, attribute.String("nfs.path", s))
			}
		}
	case "FS.MkdirAll":
		if len(args) > 0 {
			if s, ok := args[0].(string); ok {
				attrs = append(attrs, attribute.String("nfs.filename", s))
			}
		}
		if len(args) > 1 {
			if perm, ok := args[1].(os.FileMode); ok {
				attrs = append(attrs, attribute.String("nfs.perm", perm.String()))
			}
		}
	case "FS.Symlink":
		if len(args) > 0 {
			if s, ok := args[0].(string); ok {
				attrs = append(attrs, attribute.String("nfs.target", s))
			}
		}
		if len(args) > 1 {
			if s, ok := args[1].(string); ok {
				attrs = append(attrs, attribute.String("nfs.link", s))
			}
		}
	case "File.Write", "File.Read":
		if len(args) > 0 {
			if p, ok := args[0].([]byte); ok {
				attrs = append(attrs, attribute.Int("nfs.len", len(p)))
			}
		}
	case "File.ReadAt":
		if len(args) > 0 {
			if p, ok := args[0].([]byte); ok {
				attrs = append(attrs, attribute.Int("nfs.len", len(p)))
			}
		}
		if len(args) > 1 {
			if offset, ok := args[1].(int64); ok {
				attrs = append(attrs, attribute.Int64("nfs.offset", offset))
			}
		}
	case "File.Seek":
		if len(args) > 0 {
			if offset, ok := args[0].(int64); ok {
				attrs = append(attrs, attribute.Int64("nfs.offset", offset))
			}
		}
		if len(args) > 1 {
			if whence, ok := args[1].(int); ok {
				attrs = append(attrs, attribute.Int("nfs.whence", whence))
			}
		}
	case "File.Truncate":
		if len(args) > 0 {
			if size, ok := args[0].(int64); ok {
				attrs = append(attrs, attribute.Int64("nfs.size", size))
			}
		}
	case "Change.Chmod":
		if len(args) > 0 {
			if s, ok := args[0].(string); ok {
				attrs = append(attrs, attribute.String("nfs.name", s))
			}
		}
		if len(args) > 1 {
			if mode, ok := args[1].(os.FileMode); ok {
				attrs = append(attrs, attribute.String("nfs.mode", mode.String()))
			}
		}
	case "Change.Lchown", "Change.Chown":
		if len(args) > 0 {
			if s, ok := args[0].(string); ok {
				attrs = append(attrs, attribute.String("nfs.name", s))
			}
		}
		if len(args) > 1 {
			if uid, ok := args[1].(int); ok {
				attrs = append(attrs, attribute.Int("nfs.uid", uid))
			}
		}
		if len(args) > 2 {
			if gid, ok := args[2].(int); ok {
				attrs = append(attrs, attribute.Int("nfs.gid", gid))
			}
		}
	case "Change.Chtimes":
		if len(args) > 0 {
			if s, ok := args[0].(string); ok {
				attrs = append(attrs, attribute.String("nfs.name", s))
			}
		}
		if len(args) > 1 {
			if atime, ok := args[1].(time.Time); ok {
				attrs = append(attrs, attribute.String("nfs.atime", atime.String()))
			}
		}
		if len(args) > 2 {
			if mtime, ok := args[2].(time.Time); ok {
				attrs = append(attrs, attribute.String("nfs.mtime", mtime.String()))
			}
		}
	case "Handler.Mount":
		if len(args) > 0 {
			if s, ok := args[0].(string); ok {
				attrs = append(attrs, attribute.String("net.conn.remote_addr", s))
			}
		}
		if len(args) > 1 {
			if s, ok := args[1].(string); ok {
				attrs = append(attrs, attribute.String("nfs.mount.dirpath", s))
			}
		}
	case "Handler.ToHandle":
		if len(args) > 0 {
			if paths, ok := args[0].([]string); ok {
				attrs = append(attrs, attribute.StringSlice("nfs.path", paths))
			}
		}
	}

	return attrs
}
