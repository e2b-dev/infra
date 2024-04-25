package grpc

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"regexp"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

var regex = regexp.MustCompile(`http[s]?://`)

type ClientConnInterface interface {
	GetState() connectivity.State
	Invoke(ctx context.Context, method string, args any, reply any, opts ...grpc.CallOption) error
	NewStream(ctx context.Context, desc *grpc.StreamDesc, method string, opts ...grpc.CallOption) (grpc.ClientStream, error)
	Close() error
}

func GetConnection(host string, port int, useTLS bool, options ...grpc.DialOption) (ClientConnInterface, error) {
	host = regex.ReplaceAllString(host, "")
	if strings.TrimSpace(host) == "" {
		fmt.Println("Host for gRPC not set, using dummy connection")

		return &DummyConn{}, nil
	}

	if strings.HasPrefix(host, "localhost") || !useTLS {
		options = append(options, grpc.WithTransportCredentials(insecure.NewCredentials()))
		conn, err := grpc.Dial(fmt.Sprintf("%s:%d", host, port), options...)
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
	conn, err := grpc.Dial(fmt.Sprintf("%s:%d", host, port), options...)

	if err != nil {
		return nil, fmt.Errorf("failed to dial: %w", err)
	}

	return conn, nil
}
