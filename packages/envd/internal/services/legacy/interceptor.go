package legacy

import (
	"context"

	"connectrpc.com/connect"
	"github.com/rs/zerolog"
)

func Convert() ConversionInterceptor {
	return ConversionInterceptor{}
}

type ConversionInterceptor struct{}

func (l ConversionInterceptor) WrapUnary(unaryFunc connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, request connect.AnyRequest) (connect.AnyResponse, error) {
		response, err := unaryFunc(ctx, request)
		if err != nil {
			return nil, err
		}

		if request.Header().Get("User-Agent") == "connect-python" {
			response = maybeConvertResponse(zerolog.Ctx(ctx), response)
		}

		return response, nil
	}
}

func (l ConversionInterceptor) WrapStreamingClient(clientFunc connect.StreamingClientFunc) connect.StreamingClientFunc {
	return clientFunc
}

func (l ConversionInterceptor) WrapStreamingHandler(handlerFunc connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		return handlerFunc(ctx, &streamConverter{conn: conn})
	}
}
