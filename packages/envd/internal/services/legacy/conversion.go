package legacy

import (
	"errors"
	"net/http"
	"reflect"

	"connectrpc.com/connect"
	"github.com/rs/zerolog"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/e2b-dev/infra/packages/envd/internal/services/spec/filesystem"
)

func convertEntryInfo(info *filesystem.EntryInfo) *EntryInfo {
	if info == nil {
		return nil
	}

	return &EntryInfo{
		Name: info.Name,
		Type: FileType(info.Type),
		Path: info.Path,
	}
}

var (
	protoConverters = make(map[reflect.Type]func(protoreflect.ProtoMessage) (connect.AnyResponse, error))
	anyConverters   = make(map[reflect.Type]func(any) any)
)

func addConverter[TIn, TOut any](fn func(TIn) TOut) {
	protoConverters[reflect.TypeFor[TIn]()] = func(a protoreflect.ProtoMessage) (connect.AnyResponse, error) {
		b, ok := a.(TIn)
		if !ok {
			return nil, ErrUnexpectedType
		}

		c := fn(b)
		return connect.NewResponse[TOut](&c), nil
	}

	anyConverters[reflect.TypeFor[TIn]()] = func(a any) any {
		b, ok := a.(TIn)
		if !ok {
			return a
		}

		c := fn(b)
		return &c
	}
}

func init() {
	addConverter(func(in *filesystem.MoveResponse) MoveResponse {
		return MoveResponse{
			Entry: convertEntryInfo(in.Entry),
		}
	})

	addConverter(func(in *filesystem.ListDirResponse) ListDirResponse {
		var old []*EntryInfo
		for _, item := range in.Entries {
			old = append(old, convertEntryInfo(item))
		}

		return ListDirResponse{
			Entries: old,
		}
	})

	addConverter(func(in *filesystem.MakeDirResponse) MakeDirResponse {
		return MakeDirResponse{
			Entry: convertEntryInfo(in.Entry),
		}
	})

	addConverter(func(in *filesystem.RemoveResponse) RemoveResponse {
		return RemoveResponse{}
	})

	addConverter(func(in *filesystem.StatResponse) StatResponse {
		return StatResponse{
			Entry: convertEntryInfo(in.Entry),
		}
	})

	addConverter(func(in *filesystem.WatchDirResponse) WatchDirResponse {
		var event isWatchDirResponse_Event

		switch e := in.Event.(type) {
		case *filesystem.WatchDirResponse_Start:
			event = &WatchDirResponse_Start{
				Start: convertStartEvent(e.Start),
			}
		case *filesystem.WatchDirResponse_Filesystem:
			event = &WatchDirResponse_Filesystem{
				Filesystem: convertFilesystemEvent(e.Filesystem),
			}
		case *filesystem.WatchDirResponse_Keepalive:
			event = &WatchDirResponse_Keepalive{
				Keepalive: convertKeepAlive(e.Keepalive),
			}
		}

		return WatchDirResponse{
			Event: event,
		}
	})

	addConverter(func(in *filesystem.CreateWatcherResponse) CreateWatcherResponse {
		return CreateWatcherResponse{
			WatcherId: in.WatcherId,
		}
	})
}

var ErrUnexpectedType = errors.New("unexpected type")

func maybeConvertValue(input any) any {
	inputType := reflect.TypeOf(input)
	convert, ok := anyConverters[inputType]
	if !ok {
		return input
	}

	return convert(input)
}

func maybeConvertResponse(logger *zerolog.Logger, response connect.AnyResponse) connect.AnyResponse {
	value := response.Any()
	pm, ok := value.(protoreflect.ProtoMessage)
	if !ok {
		logger.Warn().
			Type("response", response).
			Msg("cannot convert, expected protoreflect.ProtoMessage")
		return response
	}

	valueType := reflect.TypeOf(pm)
	conversion, ok := protoConverters[valueType]
	if !ok {
		return response
	}

	if r, err := conversion(pm); err != nil {
		logger.Warn().
			Err(err).
			Type("response", response).
			Msg("conversion failed")
	} else {
		copyHeaders(response.Header(), r.Header())
		copyHeaders(response.Trailer(), r.Trailer())
		response = r

	}

	return response
}

func copyHeaders(src, dst http.Header) {
	for key, values := range src {
		dst[key] = values
	}
}

// Helper functions for WatchDirResponse conversion
func convertStartEvent(e *filesystem.WatchDirResponse_StartEvent) *WatchDirResponse_StartEvent {
	if e == nil {
		return nil
	}
	return &WatchDirResponse_StartEvent{}
}

func convertFilesystemEvent(e *filesystem.FilesystemEvent) *FilesystemEvent {
	if e == nil {
		return nil
	}
	return &FilesystemEvent{
		Name: e.Name,
		Type: EventType(e.Type),
	}
}

func convertKeepAlive(e *filesystem.WatchDirResponse_KeepAlive) *WatchDirResponse_KeepAlive {
	if e == nil {
		return nil
	}
	return &WatchDirResponse_KeepAlive{}
}
