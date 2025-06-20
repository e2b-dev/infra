package grpc

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"regexp"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/backoff"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

var regex = regexp.MustCompile(`http[s]?://`)

// TODO: Fix Host <-> Url
func GetConnection(host string, safe bool, options ...grpc.DialOption) (*grpc.ClientConn, error) {
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
