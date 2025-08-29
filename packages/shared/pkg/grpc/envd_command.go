package grpc

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"

	"connectrpc.com/connect"

	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
)

func StreamToChannel[Res any](ctx context.Context, stream *connect.ServerStreamForClient[Res]) (<-chan *Res, <-chan error) {
	out := make(chan *Res)
	errCh := make(chan error, 1)

	go func() {
		defer close(out)
		defer close(errCh)

		for stream.Receive() {
			select {
			case <-ctx.Done():
				// Context canceled, exit the goroutine
				return
			case out <- stream.Msg():
				// Send the message to the channel
			}
		}

		if err := stream.Err(); err != nil {
			errCh <- err
			return
		}
	}()

	return out, errCh
}

func SetSandboxHeader(header http.Header, hostname string, sandboxID string) error {
	domain, err := extractDomain(hostname)
	if err != nil {
		return fmt.Errorf("failed to extract domain from hostname: %w", err)
	}
	// Construct the host (<port>-<sandbox id>-<old client id>.e2b.app)
	host := fmt.Sprintf("%d-%s-00000000.%s", consts.DefaultEnvdServerPort, sandboxID, domain)

	header.Set("Host", host)

	return nil
}

func SetUserHeader(header http.Header, user string) {
	userString := fmt.Sprintf("%s:", user)
	userBase64 := base64.StdEncoding.EncodeToString([]byte(userString))
	basic := fmt.Sprintf("Basic %s", userBase64)
	header.Set("Authorization", basic)
}

func SetAccessTokenHeader(header http.Header, accessToken string) {
	header.Set("X-Access-Token", accessToken)
}

func extractDomain(input string) (string, error) {
	parsedURL, err := url.Parse(input)
	if err != nil || parsedURL.Host == "" {
		return "", fmt.Errorf("invalid URL: %s", input)
	}

	return parsedURL.Hostname(), nil
}
