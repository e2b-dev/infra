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
		Type: convertFileType(info.Type),
		Path: info.Path,
	}
}

func convertFileType(fileType filesystem.FileType) FileType {
	switch fileType {
	case filesystem.FileType_FILE_TYPE_FILE:
		return FileType_FILE_TYPE_DIRECTORY
	case filesystem.FileType_FILE_TYPE_DIRECTORY:
		return FileType_FILE_TYPE_DIRECTORY
	default:
		return FileType_FILE_TYPE_UNSPECIFIED
	}
}

func Convert() connect.Interceptor {
	return legacyConversion{
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
		},
	}
}

type legacyConversion struct {
	converters map[reflect.Type]func(protoreflect.ProtoMessage) connect.AnyResponse
}

func (l legacyConversion) WrapUnary(unaryFunc connect.UnaryFunc) connect.UnaryFunc {
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

func (l legacyConversion) WrapStreamingClient(clientFunc connect.StreamingClientFunc) connect.StreamingClientFunc {
	return clientFunc
}

func (l legacyConversion) WrapStreamingHandler(handlerFunc connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return handlerFunc
}

func (l legacyConversion) maybeConvert(response connect.AnyResponse) connect.AnyResponse {
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
