package o11y

import (
	"errors"
	"os"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/nfsproxy/middleware"
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

// extractArgFields extracts the relevant fields from a typed request.
// This is the single source of truth for which args to extract for each operation.
func extractArgFields(req middleware.Request) []argField {
	var fields []argField

	switch r := req.(type) {
	// Filesystem operations
	case middleware.FSCreateRequest:
		fields = append(fields, argField{"nfs.filename", r.Filename})
	case middleware.FSOpenRequest:
		fields = append(fields, argField{"nfs.filename", r.Filename})
	case middleware.FSStatRequest:
		fields = append(fields, argField{"nfs.filename", r.Filename})
	case middleware.FSLstatRequest:
		fields = append(fields, argField{"nfs.filename", r.Filename})
	case middleware.FSRemoveRequest:
		fields = append(fields, argField{"nfs.filename", r.Filename})
	case middleware.FSReadlinkRequest:
		fields = append(fields, argField{"nfs.filename", r.Link})
	case middleware.FSRenameRequest:
		fields = append(fields, argField{"nfs.oldpath", r.OldPath})
		fields = append(fields, argField{"nfs.newpath", r.NewPath})
	case middleware.FSOpenFileRequest:
		fields = append(fields, argField{"nfs.filename", r.Filename})
		fields = append(fields, argField{"nfs.flag", r.Flag})
		fields = append(fields, argField{"nfs.perm", r.Perm.String()})
	case middleware.FSTempFileRequest:
		fields = append(fields, argField{"nfs.dir", r.Dir})
		fields = append(fields, argField{"nfs.prefix", r.Prefix})
	case middleware.FSReadDirRequest:
		fields = append(fields, argField{"nfs.path", r.Path})
	case middleware.FSChrootRequest:
		fields = append(fields, argField{"nfs.path", r.Path})
	case middleware.FSMkdirAllRequest:
		fields = append(fields, argField{"nfs.filename", r.Filename})
		fields = append(fields, argField{"nfs.perm", r.Perm.String()})
	case middleware.FSSymlinkRequest:
		fields = append(fields, argField{"nfs.target", r.Target})
		fields = append(fields, argField{"nfs.link", r.Link})

	// File operations
	case middleware.FileWriteRequest:
		fields = append(fields, argField{"nfs.len", byteLen(len(r.Data))})
	case middleware.FileReadRequest:
		fields = append(fields, argField{"nfs.len", byteLen(len(r.Buffer))})
	case middleware.FileReadAtRequest:
		fields = append(fields, argField{"nfs.len", byteLen(len(r.Buffer))})
		fields = append(fields, argField{"nfs.offset", r.Offset})
	case middleware.FileSeekRequest:
		fields = append(fields, argField{"nfs.offset", r.Offset})
		fields = append(fields, argField{"nfs.whence", r.Whence})
	case middleware.FileTruncateRequest:
		fields = append(fields, argField{"nfs.size", r.Size})

	// Change operations
	case middleware.ChangeChmodRequest:
		fields = append(fields, argField{"nfs.name", r.Name})
		fields = append(fields, argField{"nfs.mode", r.Mode.String()})
	case middleware.ChangeLchownRequest:
		fields = append(fields, argField{"nfs.name", r.Name})
		fields = append(fields, argField{"nfs.uid", r.UID})
		fields = append(fields, argField{"nfs.gid", r.GID})
	case middleware.ChangeChownRequest:
		fields = append(fields, argField{"nfs.name", r.Name})
		fields = append(fields, argField{"nfs.uid", r.UID})
		fields = append(fields, argField{"nfs.gid", r.GID})
	case middleware.ChangeChtimesRequest:
		fields = append(fields, argField{"nfs.name", r.Name})
		fields = append(fields, argField{"nfs.atime", r.ATime})
		fields = append(fields, argField{"nfs.mtime", r.MTime})

	// Handler operations
	case middleware.HandlerMountRequest:
		fields = append(fields, argField{"net.conn.remote_addr", r.RemoteAddr})
		fields = append(fields, argField{"nfs.mount.dirpath", r.Dirpath})
	case middleware.HandlerToHandleRequest:
		fields = append(fields, argField{"nfs.path", r.Path})
	}

	return fields
}

// argsToAttrs converts a typed request to OpenTelemetry attributes for tracing.
func argsToAttrs(req middleware.Request) []attribute.KeyValue {
	fields := extractArgFields(req)
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

// argsToZapFields converts a typed request to zap fields for logging.
func argsToZapFields(req middleware.Request) []zap.Field {
	fields := extractArgFields(req)
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
