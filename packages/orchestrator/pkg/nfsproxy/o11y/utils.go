package o11y

import (
	"errors"
	"os"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
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

// argField represents a single extracted argument field with its typed value.
// This is the intermediate representation used by both argsToAttrs and argsToZapFields.
type argField struct {
	key   string
	value any // string, int, int64, []string, time.Time, or byteLen
}

// byteLen is a wrapper to indicate we want to log the length of bytes, not the bytes themselves.
type byteLen int

// extractArgFields extracts the relevant fields from operation arguments.
// This is the single source of truth for which args to extract for each operation.
func extractArgFields(op string, args []any) []argField {
	var fields []argField

	switch op {
	case "FS.Create", "FS.Open", "FS.Stat", "FS.Lstat", "FS.Remove", "FS.Readlink":
		if len(args) > 0 {
			if s, ok := args[0].(string); ok {
				fields = append(fields, argField{"nfs.filename", s})
			}
		}
	case "FS.Rename":
		if len(args) > 0 {
			if s, ok := args[0].(string); ok {
				fields = append(fields, argField{"nfs.oldpath", s})
			}
		}
		if len(args) > 1 {
			if s, ok := args[1].(string); ok {
				fields = append(fields, argField{"nfs.newpath", s})
			}
		}
	case "FS.OpenFile":
		if len(args) > 0 {
			if s, ok := args[0].(string); ok {
				fields = append(fields, argField{"nfs.filename", s})
			}
		}
		if len(args) > 1 {
			if flag, ok := args[1].(int); ok {
				fields = append(fields, argField{"nfs.flag", flag})
			}
		}
		if len(args) > 2 {
			if perm, ok := args[2].(os.FileMode); ok {
				fields = append(fields, argField{"nfs.perm", perm.String()})
			}
		}
	case "FS.TempFile":
		if len(args) > 0 {
			if s, ok := args[0].(string); ok {
				fields = append(fields, argField{"nfs.dir", s})
			}
		}
		if len(args) > 1 {
			if s, ok := args[1].(string); ok {
				fields = append(fields, argField{"nfs.prefix", s})
			}
		}
	case "FS.ReadDir", "FS.Chroot":
		if len(args) > 0 {
			if s, ok := args[0].(string); ok {
				fields = append(fields, argField{"nfs.path", s})
			}
		}
	case "FS.MkdirAll":
		if len(args) > 0 {
			if s, ok := args[0].(string); ok {
				fields = append(fields, argField{"nfs.filename", s})
			}
		}
		if len(args) > 1 {
			if perm, ok := args[1].(os.FileMode); ok {
				fields = append(fields, argField{"nfs.perm", perm.String()})
			}
		}
	case "FS.Symlink":
		if len(args) > 0 {
			if s, ok := args[0].(string); ok {
				fields = append(fields, argField{"nfs.target", s})
			}
		}
		if len(args) > 1 {
			if s, ok := args[1].(string); ok {
				fields = append(fields, argField{"nfs.link", s})
			}
		}
	case "File.Write", "File.Read":
		if len(args) > 0 {
			if p, ok := args[0].([]byte); ok {
				fields = append(fields, argField{"nfs.len", byteLen(len(p))})
			}
		}
	case "File.ReadAt":
		if len(args) > 0 {
			if p, ok := args[0].([]byte); ok {
				fields = append(fields, argField{"nfs.len", byteLen(len(p))})
			}
		}
		if len(args) > 1 {
			if offset, ok := args[1].(int64); ok {
				fields = append(fields, argField{"nfs.offset", offset})
			}
		}
	case "File.Seek":
		if len(args) > 0 {
			if offset, ok := args[0].(int64); ok {
				fields = append(fields, argField{"nfs.offset", offset})
			}
		}
		if len(args) > 1 {
			if whence, ok := args[1].(int); ok {
				fields = append(fields, argField{"nfs.whence", whence})
			}
		}
	case "File.Truncate":
		if len(args) > 0 {
			if size, ok := args[0].(int64); ok {
				fields = append(fields, argField{"nfs.size", size})
			}
		}
	case "Change.Chmod":
		if len(args) > 0 {
			if s, ok := args[0].(string); ok {
				fields = append(fields, argField{"nfs.name", s})
			}
		}
		if len(args) > 1 {
			if mode, ok := args[1].(os.FileMode); ok {
				fields = append(fields, argField{"nfs.mode", mode.String()})
			}
		}
	case "Change.Lchown", "Change.Chown":
		if len(args) > 0 {
			if s, ok := args[0].(string); ok {
				fields = append(fields, argField{"nfs.name", s})
			}
		}
		if len(args) > 1 {
			if uid, ok := args[1].(int); ok {
				fields = append(fields, argField{"nfs.uid", uid})
			}
		}
		if len(args) > 2 {
			if gid, ok := args[2].(int); ok {
				fields = append(fields, argField{"nfs.gid", gid})
			}
		}
	case "Change.Chtimes":
		if len(args) > 0 {
			if s, ok := args[0].(string); ok {
				fields = append(fields, argField{"nfs.name", s})
			}
		}
		if len(args) > 1 {
			if atime, ok := args[1].(time.Time); ok {
				fields = append(fields, argField{"nfs.atime", atime})
			}
		}
		if len(args) > 2 {
			if mtime, ok := args[2].(time.Time); ok {
				fields = append(fields, argField{"nfs.mtime", mtime})
			}
		}
	case "Handler.Mount":
		if len(args) > 0 {
			if s, ok := args[0].(string); ok {
				fields = append(fields, argField{"net.conn.remote_addr", s})
			}
		}
		if len(args) > 1 {
			if s, ok := args[1].(string); ok {
				fields = append(fields, argField{"nfs.mount.dirpath", s})
			}
		}
	case "Handler.ToHandle":
		if len(args) > 0 {
			if paths, ok := args[0].([]string); ok {
				fields = append(fields, argField{"nfs.path", paths})
			}
		}
	}

	return fields
}

// argsToAttrs converts operation arguments to OpenTelemetry attributes for tracing.
func argsToAttrs(op string, args []any) []attribute.KeyValue {
	fields := extractArgFields(op, args)
	attrs := make([]attribute.KeyValue, 0, len(fields))

	for _, f := range fields {
		switch v := f.value.(type) {
		case string:
			attrs = append(attrs, attribute.String(f.key, v))
		case int:
			attrs = append(attrs, attribute.Int(f.key, v))
		case int64:
			attrs = append(attrs, attribute.Int64(f.key, v))
		case byteLen:
			attrs = append(attrs, attribute.Int(f.key, int(v)))
		case []string:
			attrs = append(attrs, attribute.StringSlice(f.key, v))
		case time.Time:
			attrs = append(attrs, attribute.String(f.key, v.String()))
		}
	}

	return attrs
}

// argsToZapFields converts operation arguments to zap fields for logging.
func argsToZapFields(op string, args []any) []zap.Field {
	fields := extractArgFields(op, args)
	zapFields := make([]zap.Field, 0, len(fields))

	for _, f := range fields {
		switch v := f.value.(type) {
		case string:
			zapFields = append(zapFields, zap.String(f.key, v))
		case int:
			zapFields = append(zapFields, zap.Int(f.key, v))
		case int64:
			zapFields = append(zapFields, zap.Int64(f.key, v))
		case byteLen:
			zapFields = append(zapFields, zap.Int(f.key, int(v)))
		case []string:
			zapFields = append(zapFields, zap.Strings(f.key, v))
		case time.Time:
			zapFields = append(zapFields, logger.Time(f.key, v))
		}
	}

	return zapFields
}
