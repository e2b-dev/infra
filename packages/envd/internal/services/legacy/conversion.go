package legacy

import (
	"context"
	"errors"
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

var ErrUnexpectedType = errors.New("unexpected type")

func Convert() ConversionInterceptor {
	return ConversionInterceptor{
		converters: map[reflect.Type]func(protoreflect.ProtoMessage) (connect.AnyResponse, error){
			reflect.TypeFor[*filesystem.MoveResponse](): func(a protoreflect.ProtoMessage) (connect.AnyResponse, error) {
				mr, ok := a.(*filesystem.MoveResponse)
				if !ok {
					return nil, ErrUnexpectedType
				}

				return connect.NewResponse(&MoveResponse{
					Entry: convertEntryInfo(mr.Entry),
				}), nil
			},
			reflect.TypeFor[*filesystem.ListDirResponse](): func(a protoreflect.ProtoMessage) (connect.AnyResponse, error) {
				mr, ok := a.(*filesystem.ListDirResponse)
				if !ok {
					return nil, ErrUnexpectedType
				}

				var old []*EntryInfo
				for _, item := range mr.Entries {
					old = append(old, convertEntryInfo(item))
				}

				return connect.NewResponse(&ListDirResponse{
					Entries: old,
				}), nil
			},
			reflect.TypeFor[*filesystem.MakeDirResponse](): func(a protoreflect.ProtoMessage) (connect.AnyResponse, error) {
				mr, ok := a.(*filesystem.MakeDirResponse)
				if !ok {
					return nil, ErrUnexpectedType
				}

				return connect.NewResponse(&MakeDirResponse{
					Entry: convertEntryInfo(mr.Entry),
				}), nil
			},
			reflect.TypeFor[*filesystem.RemoveResponse](): func(a protoreflect.ProtoMessage) (connect.AnyResponse, error) {
				_, ok := a.(*filesystem.RemoveResponse)
				if !ok {
					return nil, ErrUnexpectedType
				}

				return connect.NewResponse(&RemoveResponse{}), nil
			},
			reflect.TypeFor[*filesystem.StatResponse](): func(a protoreflect.ProtoMessage) (connect.AnyResponse, error) {
				sr, ok := a.(*filesystem.StatResponse)
				if !ok {
					return nil, ErrUnexpectedType
				}

				return connect.NewResponse(&StatResponse{
					Entry: convertEntryInfo(sr.Entry),
				}), nil
			},
			reflect.TypeFor[*filesystem.WatchDirResponse](): func(a protoreflect.ProtoMessage) (connect.AnyResponse, error) {
				wr, ok := a.(*filesystem.WatchDirResponse)
				if !ok {
					return nil, ErrUnexpectedType
				}

				response := &WatchDirResponse{}

				switch e := wr.Event.(type) {
				case *filesystem.WatchDirResponse_Start:
					response.Event = &WatchDirResponse_Start{
						Start: convertStartEvent(e.Start),
					}
				case *filesystem.WatchDirResponse_Filesystem:
					response.Event = &WatchDirResponse_Filesystem{
						Filesystem: convertFilesystemEvent(e.Filesystem),
					}
				case *filesystem.WatchDirResponse_Keepalive:
					response.Event = &WatchDirResponse_Keepalive{
						Keepalive: convertKeepAlive(e.Keepalive),
					}
				}

				return connect.NewResponse(response), nil
			},
			reflect.TypeFor[*filesystem.CreateWatcherResponse](): func(a protoreflect.ProtoMessage) (connect.AnyResponse, error) {
				cr, ok := a.(*filesystem.CreateWatcherResponse)
				if !ok {
					return nil, ErrUnexpectedType
				}

				return connect.NewResponse(&CreateWatcherResponse{
					WatcherId: cr.WatcherId,
				}), nil
			},
		},
	}
}

type ConversionInterceptor struct {
	converters map[reflect.Type]func(protoreflect.ProtoMessage) (connect.AnyResponse, error)
}

func (l ConversionInterceptor) WrapUnary(unaryFunc connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, request connect.AnyRequest) (connect.AnyResponse, error) {
		response, err := unaryFunc(ctx, request)
		if err != nil {
			return nil, err
		}

		if request.Header().Get("User-Agent") == "connect-python" {
			response = l.maybeConvert(ctx, response)
		}

		return response, nil
	}
}

func (l ConversionInterceptor) WrapStreamingClient(clientFunc connect.StreamingClientFunc) connect.StreamingClientFunc {
	return clientFunc
}

func (l ConversionInterceptor) WrapStreamingHandler(handlerFunc connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return handlerFunc
}

var ErrExpectedProtoMessage = errors.New("expected proto message")

func (l ConversionInterceptor) maybeConvert(ctx context.Context, response connect.AnyResponse) connect.AnyResponse {
	value := response.Any()
	pm, ok := value.(protoreflect.ProtoMessage)
	if !ok {
		zerolog.Ctx(ctx).Warn().
			Type("response", response).
			Msg("cannot convert, expected protoreflect.ProtoMessage")
		return response
	}

	valueType := reflect.TypeOf(pm)
	conversion, ok := l.converters[valueType]
	if !ok {
		return response
	}

	if r, err := conversion(pm); err != nil {
		zerolog.Ctx(ctx).Warn().
			Err(err).
			Type("response", response).
			Msg("conversion failed")
	} else {
		response = r
	}

	return response
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
