package feature_flags

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/launchdarkly/go-sdk-common/v3/ldcontext"
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
		for flag, value := range flagsBool {
			LaunchDarklyOfflineStore.Flag(string(flag)).VariationForAll(value)
		}

		for flag, value := range flagsInt {
			LaunchDarklyOfflineStore.Flag(string(flag)).ValueForAll(ldvalue.Int(value))
		}

		// waitFor has to be 0 for offline store
		ldClient, err = ldclient.MakeCustomClient("", ldclient.Config{DataSource: LaunchDarklyOfflineStore}, 0)
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

func (c *Client) BoolFlag(flagName BoolFlag, evalKey string) (bool, error) {
	defaultValue := flagsBool[flagName]

	if c.ld == nil {
		return defaultValue, fmt.Errorf("LaunchDarkly client is not initialized")
	}

	flagCtx := ldcontext.NewBuilder(string(flagName)).SetString("evalKey", evalKey).Build()
	enabled, err := c.ld.BoolVariation(string(flagName), flagCtx, defaultValue)
	if err != nil {
		return enabled, fmt.Errorf("error evaluating %s: %w", flagName, err)
	}

	return enabled, nil
}

func (c *Client) IntFlag(flagName IntFlag, evalKey string) (int, error) {
	defaultValue := flagsInt[flagName]
	if c.ld == nil {
		return defaultValue, fmt.Errorf("LaunchDarkly client is not initialized")
	}

	flagCtx := ldcontext.NewBuilder(string(flagName)).SetString("evalKey", evalKey).Build()
	value, err := c.ld.IntVariation(string(flagName), flagCtx, defaultValue)
	if err != nil {
		return value, fmt.Errorf("error evaluating %s: %w", flagName, err)
	}

	return value, nil
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
