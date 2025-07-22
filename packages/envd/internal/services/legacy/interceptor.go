package legacy

import (
	"context"
	"net/http"

	"connectrpc.com/connect"
	"github.com/rs/zerolog"
)

const brokenUserAgent = "connect-python"

var notifyHeader = http.CanonicalHeaderKey("x-e2b-legacy-sdk")

func shouldHideChanges(request http.Header, response http.Header) bool {
	if request.Get("user-agent") != brokenUserAgent {
		return false
	}

	response.Set(notifyHeader, "true")
	return true
}

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

		if shouldHideChanges(request.Header(), response.Header()) {
			response = maybeConvertResponse(zerolog.Ctx(ctx), response)
		}

		return response, nil
	}
}

func (l ConversionInterceptor) WrapStreamingClient(clientFunc connect.StreamingClientFunc) connect.StreamingClientFunc {
	// only used for client, not server
	panic("unused by server")
}

func (l ConversionInterceptor) WrapStreamingHandler(handlerFunc connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		if shouldHideChanges(conn.RequestHeader(), conn.ResponseHeader()) {
			conn = &streamConverter{conn: conn}
		}

		return handlerFunc(ctx, conn)
	}
}
