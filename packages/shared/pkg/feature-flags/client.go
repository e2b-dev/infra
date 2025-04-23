package feature_flags

import (
	ldclient "github.com/launchdarkly/go-server-sdk/v7"
	"github.com/launchdarkly/go-server-sdk/v7/testhelpers/ldtestdata"
	"go.uber.org/zap"
	"os"
	"time"
)

// LaunchDarklyOfflineStore is a test fixture that provides dynamically updatable feature flag state
var LaunchDarklyOfflineStore = ldtestdata.DataSource()

var launchDarklyApiKey = os.Getenv("LAUNCH_DARKLY_API_KEY")

type Client struct {
	Ld *ldclient.LDClient
}

func NewClient(waitForInitialize time.Duration) (*Client, error) {
	var ldClient *ldclient.LDClient
	var err error

	if launchDarklyApiKey == "" {
		ldClient, err = ldclient.MakeCustomClient("", ldclient.Config{DataSource: LaunchDarklyOfflineStore}, waitForInitialize)
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
	if c.Ld == nil {
		return nil
	}

	err := c.Ld.Close()
	if err != nil {
		zap.L().Error("Error during launch-darkly client shutdown", zap.Error(err))
		return err
	}

	return nil
}
