package legacy

import (
	"connectrpc.com/connect"
	"context"
	"github.com/e2b-dev/infra/packages/envd/internal/services/spec/filesystem"
	"google.golang.org/protobuf/reflect/protoreflect"
	"reflect"
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

func Convert() ConversionInterceptor {
	return ConversionInterceptor{
		converters: map[reflect.Type]func(protoreflect.ProtoMessage) connect.AnyResponse{
			reflect.TypeFor[*filesystem.MoveResponse](): func(a protoreflect.ProtoMessage) connect.AnyResponse {
				mr, ok := a.(*filesystem.MoveResponse)
				if !ok {
					panic("wrong type")
				}

				return connect.NewResponse(&MoveResponse{
					Entry: convertEntryInfo(mr.Entry),
				})
			},
			reflect.TypeFor[*filesystem.ListDirResponse](): func(a protoreflect.ProtoMessage) connect.AnyResponse {
				mr, ok := a.(*filesystem.ListDirResponse)
				if !ok {
					panic("wrong type")
				}

				var old []*EntryInfo
				for _, item := range mr.Entries {
					old = append(old, convertEntryInfo(item))
				}

				return connect.NewResponse(&ListDirResponse{
					Entries: old,
				})
			},
			reflect.TypeFor[*filesystem.MakeDirResponse](): func(a protoreflect.ProtoMessage) connect.AnyResponse {
				mr, ok := a.(*filesystem.MakeDirResponse)
				if !ok {
					panic("wrong type")
				}

				return connect.NewResponse(&MakeDirResponse{
					Entry: convertEntryInfo(mr.Entry),
				})
			},
			reflect.TypeFor[*filesystem.RemoveResponse](): func(a protoreflect.ProtoMessage) connect.AnyResponse {
				_, ok := a.(*filesystem.RemoveResponse)
				if !ok {
					panic("wrong type")
				}

				return connect.NewResponse(&RemoveResponse{})
			},
			reflect.TypeFor[*filesystem.StatResponse](): func(a protoreflect.ProtoMessage) connect.AnyResponse {
				sr, ok := a.(*filesystem.StatResponse)
				if !ok {
					panic("wrong type")
				}

				return connect.NewResponse(&StatResponse{
					Entry: convertEntryInfo(sr.Entry),
				})
			},
			reflect.TypeFor[*filesystem.WatchDirResponse](): func(a protoreflect.ProtoMessage) connect.AnyResponse {
				wr, ok := a.(*filesystem.WatchDirResponse)
				if !ok {
					panic("wrong type")
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

				return connect.NewResponse(response)
			},
			reflect.TypeFor[*filesystem.CreateWatcherResponse](): func(a protoreflect.ProtoMessage) connect.AnyResponse {
				cr, ok := a.(*filesystem.CreateWatcherResponse)
				if !ok {
					panic("wrong type")
				}

				return connect.NewResponse(&CreateWatcherResponse{
					WatcherId: cr.WatcherId,
				})
			},
		},
	}
}

type ConversionInterceptor struct {
	converters map[reflect.Type]func(protoreflect.ProtoMessage) connect.AnyResponse
}

func (l ConversionInterceptor) WrapUnary(unaryFunc connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, request connect.AnyRequest) (connect.AnyResponse, error) {
		response, err := unaryFunc(ctx, request)
		if err != nil {
			return nil, err
		}

		if request.Header().Get("User-Agent") == "connect-python" {
			response = l.maybeConvert(response)
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

func (l ConversionInterceptor) maybeConvert(response connect.AnyResponse) connect.AnyResponse {
	value := response.Any()
	pm, ok := value.(protoreflect.ProtoMessage)
	if !ok {
		return response
	}

	valueType := reflect.TypeOf(pm)
	conversion, ok := l.converters[valueType]
	if !ok {
		return response
	}

	return conversion(pm)
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
