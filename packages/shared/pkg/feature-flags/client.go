package feature_flags

import (
	"context"
	"fmt"
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
	ld *ldclient.LDClient
}

func NewClient() (*Client, error) {
	var ldClient *ldclient.LDClient
	var err error

	if launchDarklyApiKey == "" {
		for flag, value := range flagsInt {
			builder := LaunchDarklyOfflineStore.Flag(string(flag)).ValueForAll(ldvalue.Int(value))
			LaunchDarklyOfflineStore.Update(builder)
		}

		// waitFor has to be 0 for offline store
		ldClient, err = ldclient.MakeCustomClient(
			"",
			ldclient.Config{
				DataSource: LaunchDarklyOfflineStore,
			},
			0)
		if err != nil {
			return nil, err
		}

		return &Client{ld: ldClient}, nil
	}

	ldClient, err = ldclient.MakeClient(launchDarklyApiKey, waitForInit)
	if err != nil {
		return nil, err
	}

	return &Client{ld: ldClient}, nil
}

func (c *Client) BoolFlag(ctx context.Context, flag BoolFlag) (bool, error) {
	if c.ld == nil {
		return flag.fallback, fmt.Errorf("LaunchDarkly client is not initialized")
	}

	embeddedCtx := getContext(ctx)

	enabled, err := c.ld.BoolVariationCtx(ctx, flag.name, embeddedCtx, flag.fallback)
	if err != nil {
		return enabled, fmt.Errorf("error evaluating %s: %w", flag, err)
	}

	return enabled, nil
}

func (c *Client) IntFlag(ctx context.Context, flagName IntFlag) (int, error) {
	defaultValue := flagsInt[flagName]
	if c.ld == nil {
		return defaultValue, fmt.Errorf("LaunchDarkly client is not initialized")
	}

	embeddedCtx := getContext(ctx)

	value, err := c.ld.IntVariationCtx(ctx, string(flagName), embeddedCtx, defaultValue)
	if err != nil {
		return value, fmt.Errorf("error evaluating %s: %w", flagName, err)
	}

	return value, nil
}

func (c *Client) IntFlagDefault(flagName IntFlag) int {
	return flagsInt[flagName]
}

func (c *Client) Close(ctx context.Context) error {
	if c.ld == nil {
		return nil
	}

	err := c.ld.Close()
	if err != nil {
		zap.L().Error("Error during launch-darkly client shutdown", zap.Error(err))
		return err
	}

	return nil
}
