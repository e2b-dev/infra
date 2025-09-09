package logs

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"

	"connectrpc.com/connect"
	"github.com/rs/zerolog"
)

const (
	DefaultHTTPMethod string = "POST"
)

var operationID = atomic.Int32{}

func AssignOperationID() string {
	id := operationID.Add(1)

	return strconv.Itoa(int(id))
}

type operationIDKey struct{}

func AddRequestIDToContext(ctx context.Context) (context.Context, string) {
	operationID := AssignOperationID()
	return context.WithValue(ctx, operationIDKey{}, operationID), operationID
}

var (
	ErrOperationIDNotInContext = errors.New("operation id not in context")
	ErrUnexpectedType          = errors.New("unexpected type")
)

func GetRequestID(ctx context.Context) (string, error) {
	value := ctx.Value(operationIDKey{})
	if value == nil {
		return "", ErrOperationIDNotInContext
	}

	requestID, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("%w: expected %T, received %T",
			ErrUnexpectedType, requestID, value)
	}

	return requestID, nil
}

func SafeGetRequestID(ctx context.Context) string {
	requestID, _ := GetRequestID(ctx)
	return requestID
}

func formatMethod(method string) string {
	parts := strings.Split(method, ".")
	if len(parts) < 2 {
		return method
	}

	split := strings.Split(parts[1], "/")
	if len(split) < 2 {
		return method
	}

	servicePart := split[0]
	servicePart = strings.ToUpper(servicePart[:1]) + servicePart[1:]

	methodPart := split[1]
	methodPart = strings.ToLower(methodPart[:1]) + methodPart[1:]

	return fmt.Sprintf("%s %s", servicePart, methodPart)
}

func NewUnaryLogInterceptor(logger *zerolog.Logger) connect.UnaryInterceptorFunc {
	interceptor := func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(
			ctx context.Context,
			req connect.AnyRequest,
		) (connect.AnyResponse, error) {
			ctx, operationID := AddRequestIDToContext(ctx)

			res, err := next(ctx, req)

			l := logger.
				Err(err).
				Str("method", DefaultHTTPMethod+" "+req.Spec().Procedure)

			l = l.Str("operation_id", string(operationID))

			if err != nil {
				l = l.Int("error_code", int(connect.CodeOf(err)))
			}

			if req != nil {
				l = l.Interface("request", req.Any())
			}

			if res != nil && err == nil {
				l = l.Interface("response", res.Any())
			}

			if res == nil && err == nil {
				l = l.Interface("response", nil)
			}

			l.Msg(formatMethod(req.Spec().Procedure))

			return res, err
		}
	}

	return interceptor
}

func LogServerStreamWithoutEvents[T any, R any](
	ctx context.Context,
	logger *zerolog.Logger,
	req *connect.Request[R],
	stream *connect.ServerStream[T],
	handler func(ctx context.Context, req *connect.Request[R], stream *connect.ServerStream[T]) error,
) error {
	ctx, operationID := AddRequestIDToContext(ctx)

	l := logger.Debug().
		Str("method", DefaultHTTPMethod+" "+req.Spec().Procedure).
		Str("operation_id", operationID).
		Interface("request", req.Any())

	l.Msg(fmt.Sprintf("%s (server stream start)", formatMethod(req.Spec().Procedure)))

	err := handler(ctx, req, stream)

	logEvent := getErrDebugLogEvent(logger, err).
		Str("operation_id", operationID)

	if err != nil {
		logEvent = logEvent.Int("error_code", int(connect.CodeOf(err)))
	} else {
		logEvent = logEvent.Interface("response", nil)
	}

	logEvent.Msg(fmt.Sprintf("%s (server stream end)", formatMethod(req.Spec().Procedure)))

	return err
}

func LogClientStreamWithoutEvents[T any, R any](
	ctx context.Context,
	logger *zerolog.Logger,
	stream *connect.ClientStream[T],
	handler func(ctx context.Context, stream *connect.ClientStream[T]) (*connect.Response[R], error),
) (*connect.Response[R], error) {
	ctx, operationID := AddRequestIDToContext(ctx)
	logger.Debug().
		Str("method", DefaultHTTPMethod+" "+stream.Spec().Procedure).
		Str("operation_id", operationID).
		Msg(fmt.Sprintf("%s (client stream start)", formatMethod(stream.Spec().Procedure)))

	res, err := handler(ctx, stream)

	logEvent := getErrDebugLogEvent(logger, err).
		Str("method", DefaultHTTPMethod+" "+stream.Spec().Procedure).
		Str("operation_id", operationID)

	if err != nil {
		logEvent = logEvent.Int("error_code", int(connect.CodeOf(err)))
	}

	if res != nil && err == nil {
		logEvent = logEvent.Interface("response", res.Any())
	}

	if res == nil && err == nil {
		logEvent = logEvent.Interface("response", nil)
	}

	logEvent.Msg(fmt.Sprintf("%s (client stream end)", formatMethod(stream.Spec().Procedure)))

	return res, err
}

// Return logger with error level if err is not nil, otherwise return logger with debug level
func getErrDebugLogEvent(logger *zerolog.Logger, err error) *zerolog.Event {
	if err != nil {
		return logger.Error().Err(err)
	}

	return logger.Debug()
}
