package legacy

import (
	"net/http"

	"connectrpc.com/connect"
	"github.com/rs/zerolog"
)

type streamConverter struct {
	conn   connect.StreamingHandlerConn
	logger *zerolog.Logger
}

func (s streamConverter) Spec() connect.Spec {
	return s.Spec()
}

func (s streamConverter) Peer() connect.Peer {
	return s.Peer()
}

func (s streamConverter) Receive(a any) error {
	return s.conn.Receive(a)
}

func (s streamConverter) RequestHeader() http.Header {
	return s.conn.RequestHeader()
}

func (s streamConverter) Send(a any) error {
	a = maybeConvertValue(a)
	return s.conn.Send(a)
}

func (s streamConverter) ResponseHeader() http.Header {
	return s.conn.ResponseHeader()
}

func (s streamConverter) ResponseTrailer() http.Header {
	return s.conn.ResponseTrailer()
}
