package feature_flags

import (
	ldclient "github.com/launchdarkly/go-server-sdk/v7"
	"go.uber.org/zap"
	"os"
	"time"
)

var launchDarklyApiKey = os.Getenv("LAUNCH_DARKLY_API_KEY")

const (
	// User by client proxy to route traffic between nginx sandbox proxy and orchestrator proxy
	// https://app.launchdarkly.com/projects/default/flags/sandbox-proxy-traffic-target
	ClientProxyTrafficTargetFeatureFlag = "sandbox-proxy-traffic-target"

	ClientProxyTrafficToNginx        = "nginx-proxy"
	ClientProxyTrafficToOrchestrator = "orchestrator-proxy"
)

type Client struct {
	Ld *ldclient.LDClient
}

func NewClient(waitForInitialize time.Duration) (*Client, error) {
	var ldClient *ldclient.LDClient
	var err error

	if launchDarklyApiKey == "" {
		ldClient, err = ldclient.MakeCustomClient("", ldclient.Config{Offline: true}, waitForInitialize)
		if err != nil {
			return nil, err
		}

		return &Client{Ld: ldClient}, nil
	}

	ldClient, err = ldclient.MakeClient(launchDarklyApiKey, waitForInitialize)
	if err != nil {
		return nil, err
	}

	return &Client{Ld: ldClient}, nil
}

func (c *Client) Close() error {
	if c.Ld != nil {
		err := c.Ld.Close()
		if err != nil {
			zap.L().Error("Error during launch-darkly client shutdown", zap.Error(err))
			return err
		}
	}

	return nil
}
