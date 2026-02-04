package feature_flags

import (
	"context"
	"os"
	"time"

	"github.com/launchdarkly/go-sdk-common/v3/ldcontext"
	"github.com/launchdarkly/go-sdk-common/v3/ldlog"
	"github.com/launchdarkly/go-sdk-common/v3/ldvalue"
	ldclient "github.com/launchdarkly/go-server-sdk/v7"
	"github.com/launchdarkly/go-server-sdk/v7/ldcomponents"
	"github.com/launchdarkly/go-server-sdk/v7/testhelpers/ldtestdata"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

// launchDarklyOfflineStore is a test fixture that provides dynamically updatable feature flag state
var launchDarklyOfflineStore = ldtestdata.DataSource()

var launchDarklyApiKey = os.Getenv("LAUNCH_DARKLY_API_KEY")

const waitForInit = 5 * time.Second

type Client struct {
	ld             *ldclient.LDClient
	deploymentName string
}

func NewClientWithDatasource(source *ldtestdata.TestDataSource) (*Client, error) {
	ldClient, err := ldclient.MakeCustomClient(
		"",
		ldclient.Config{
			DataSource: source,
		},
		0)
	if err != nil {
		return nil, err
	}

	return &Client{ld: ldClient}, nil
}

func NewClient() (*Client, error) {
	if launchDarklyApiKey == "" {
		return NewClientWithDatasource(launchDarklyOfflineStore)
	}

	ldClient, err := ldclient.MakeClient(launchDarklyApiKey, waitForInit)
	if err != nil {
		return nil, err
	}

	return &Client{ld: ldClient}, nil
}

// NewClientWithLogLevel creates a client with a specific log level.
// Use ldlog.Error to suppress INFO/WARN logs in CLI tools.
func NewClientWithLogLevel(logLevel ldlog.LogLevel) (*Client, error) {
	cfg := ldclient.Config{
		Logging: ldcomponents.Logging().MinLevel(logLevel),
	}

	if launchDarklyApiKey == "" {
		cfg.DataSource = launchDarklyOfflineStore
		ldClient, err := ldclient.MakeCustomClient("", cfg, 0)
		if err != nil {
			return nil, err
		}

		return &Client{ld: ldClient}, nil
	}

	ldClient, err := ldclient.MakeCustomClient(launchDarklyApiKey, cfg, waitForInit)
	if err != nil {
		return nil, err
	}

	return &Client{ld: ldClient}, nil
}

func (c *Client) SetDeploymentName(deploymentName string) {
	c.deploymentName = deploymentName
}

func (c *Client) BoolFlag(ctx context.Context, flag BoolFlag, contexts ...ldcontext.Context) bool {
	return getFlag(ctx, c.ld, c.ld.BoolVariationCtx, flag, c.allContexts(contexts))
}

func (c *Client) JSONFlag(ctx context.Context, flag JSONFlag, contexts ...ldcontext.Context) ldvalue.Value {
	return getFlag(ctx, c.ld, c.ld.JSONVariationCtx, flag, c.allContexts(contexts))
}

func (c *Client) IntFlag(ctx context.Context, flag IntFlag, contexts ...ldcontext.Context) int {
	return getFlag(ctx, c.ld, c.ld.IntVariationCtx, flag, c.allContexts(contexts))
}

func (c *Client) StringFlag(ctx context.Context, flag StringFlag, contexts ...ldcontext.Context) string {
	return getFlag(ctx, c.ld, c.ld.StringVariationCtx, flag, c.allContexts(contexts))
}

type typedFlag[T any] interface {
	Key() string
	Fallback() T
}

func getFlag[T any](
	ctx context.Context,
	ld *ldclient.LDClient,
	getFromLaunchDarkly func(ctx context.Context, key string, context ldcontext.Context, defaultVal T) (T, error),
	flag typedFlag[T],
	contexts []ldcontext.Context,
) T {
	if ld == nil {
		logger.L().Info(ctx, "LaunchDarkly client is not initialized, returning fallback")

		return flag.Fallback()
	}

	value, err := getFromLaunchDarkly(ctx, flag.Key(), mergeContexts(ctx, contexts), flag.Fallback())
	if err != nil {
		logger.L().Warn(ctx, "error evaluating flag", zap.Error(err), zap.String("flag", flag.Key()))
	}

	return value
}

func (c *Client) Close(ctx context.Context) error {
	if c.ld == nil {
		return nil
	}

	err := c.ld.Close()
	if err != nil {
		logger.L().Error(ctx, "Error during launch-darkly client shutdown", zap.Error(err))

		return err
	}

	return nil
}

func (c *Client) allContexts(contexts []ldcontext.Context) []ldcontext.Context {
	if c.deploymentName != "" {
		contexts = append(contexts, deploymentContext(c.deploymentName))
	}

	return contexts
}
