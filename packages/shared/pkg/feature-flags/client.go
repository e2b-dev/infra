package feature_flags

import (
	"context"
	"os"
	"time"

	"github.com/launchdarkly/go-sdk-common/v3/ldvalue"
	ldclient "github.com/launchdarkly/go-server-sdk/v7"
	"github.com/launchdarkly/go-server-sdk/v7/testhelpers/ldtestdata"
	"go.uber.org/zap"
)

// LaunchDarklyOfflineStore is a test fixture that provides dynamically updatable feature flag state
var LaunchDarklyOfflineStore = ldtestdata.DataSource()

var launchDarklyApiKey = os.Getenv("LAUNCH_DARKLY_API_KEY")

const waitForInit = 5 * time.Second

type Client struct {
	Ld *ldclient.LDClient
}

func NewClient() (*Client, error) {
	var ldClient *ldclient.LDClient
	var err error

	if launchDarklyApiKey == "" {
		LaunchDarklyOfflineStore.Flag(GcloudMaxCPUQuota).ValueForAll(ldvalue.Int(GcloudMaxCPUQuotaDefault))
		LaunchDarklyOfflineStore.Flag(GcloudMaxMemoryLimitMiB).ValueForAll(ldvalue.Int(GcloudMaxMemoryLimitMiBDefault))
		LaunchDarklyOfflineStore.Flag(GcloudConcurrentUploadLimit).ValueForAll(ldvalue.Int(GcloudConcurrentUploadLimitDefault))
		LaunchDarklyOfflineStore.Flag(GcloudMaxTasks).ValueForAll(ldvalue.Int(GcloudMaxTasksDefault))

		// waitFor has to be 0 for offline store
		ldClient, err = ldclient.MakeCustomClient("", ldclient.Config{DataSource: LaunchDarklyOfflineStore}, 0)
		if err != nil {
			return nil, err
		}

		return &Client{Ld: ldClient}, nil
	}

	ldClient, err = ldclient.MakeClient(launchDarklyApiKey, waitForInit)
	if err != nil {
		return nil, err
	}

	return &Client{Ld: ldClient}, nil
}

func (c *Client) Close(ctx context.Context) error {
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
