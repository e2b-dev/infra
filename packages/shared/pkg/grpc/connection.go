package grpc

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"regexp"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/backoff"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

var regex = regexp.MustCompile(`http[s]?://`)

type ClientConnInterface interface {
	Invoke(ctx context.Context, method string, args any, reply any, opts ...grpc.CallOption) error
	GetState() connectivity.State
	NewStream(ctx context.Context, desc *grpc.StreamDesc, method string, opts ...grpc.CallOption) (grpc.ClientStream, error)
	Close() error
}

// TODO: Fix Host <-> Url
func GetConnection(host string, safe bool, options ...grpc.DialOption) (ClientConnInterface, error) {
	if strings.TrimSpace(host) == "" {
		fmt.Println("Host for gRPC not set, using dummy connection")

		return &DummyConn{}, nil
	}

	options = append(options, grpc.WithConnectParams(grpc.ConnectParams{Backoff: backoff.DefaultConfig}))

	host = regex.ReplaceAllString(host, "")
	if strings.HasPrefix(host, "localhost") || !safe {
		options = append(options, grpc.WithTransportCredentials(insecure.NewCredentials()))
		conn, err := grpc.Dial(host, options...)
		if err != nil {
			return nil, fmt.Errorf("failed to dial: %w", err)
		}

		return conn, nil
	}

	systemRoots, err := x509.SystemCertPool()
	if err != nil {
		errMsg := fmt.Errorf("failed to read system root certificate pool: %w", err)

		return nil, errMsg
	}

	cred := credentials.NewTLS(&tls.Config{
		RootCAs:    systemRoots,
		MinVersion: tls.VersionTLS13,
	})

	options = append(options, grpc.WithAuthority(host), grpc.WithTransportCredentials(cred))
	conn, err := grpc.Dial(host+":443", options...)

	if err != nil {
		return nil, fmt.Errorf("failed to dial: %w", err)
	}

	return conn, nil
}
