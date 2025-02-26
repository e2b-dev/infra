package analyticscollector

import (
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/posthog/posthog-go"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/utils"
)

const (
	teamGroup                = "team"
	placeholderTeamGroupUser = "backend"
	placeholderProperty      = "first interaction"

	infraVersionKey = "infra_version"
	infraVersion    = "v1"
)

type PosthogClient struct {
	client posthog.Client
}

func NewPosthogClient() (*PosthogClient, error) {
	posthogAPIKey := os.Getenv("POSTHOG_API_KEY")
	posthogLogger := posthog.StdLogger(log.New(os.Stderr, "posthog ", log.LstdFlags))

	if strings.TrimSpace(posthogAPIKey) == "" {
		zap.L().Info("No Posthog API key provided, silencing logs")

		writer := &utils.NoOpWriter{}
		posthogLogger = posthog.StdLogger(log.New(writer, "posthog ", log.LstdFlags))
	}

	client, err := posthog.NewWithConfig(posthogAPIKey, posthog.Config{
		Interval:  30 * time.Second,
		BatchSize: 100,
		Verbose:   false,
		Logger:    posthogLogger,
	})

	if err != nil {
		zap.L().Fatal("error initializing Posthog client", zap.Error(err))
	}

	return &PosthogClient{
		client: client,
	}, nil
}

func (p *PosthogClient) Close() error {
	return p.client.Close()
}

func (p *PosthogClient) IdentifyAnalyticsTeam(teamID string, teamName string) {
	err := p.client.Enqueue(posthog.GroupIdentify{
		Type: teamGroup,
		Key:  teamID,
		Properties: posthog.NewProperties().
			Set(placeholderProperty, true).
			Set("name", teamName),
	},
	)
	if err != nil {
		zap.L().Error("error when setting group property in Posthog", zap.Error(err))
	}
}

func (p *PosthogClient) CreateAnalyticsTeamEvent(teamID, event string, properties posthog.Properties) {
	err := p.client.Enqueue(posthog.Capture{
		DistinctId: placeholderTeamGroupUser,
		Event:      event,
		Properties: properties.Set(infraVersionKey, infraVersion),
		Groups: posthog.NewGroups().
			Set("team", teamID),
	})
	if err != nil {
		zap.L().Error("error when sending event to Posthog", zap.Error(err))
	}
}

func (p *PosthogClient) CreateAnalyticsUserEvent(userID string, teamID string, event string, properties posthog.Properties) {
	err := p.client.Enqueue(posthog.Capture{
		DistinctId: userID,
		Event:      event,
		Properties: properties.Set(infraVersionKey, infraVersion),
		Groups: posthog.NewGroups().
			Set("team", teamID),
	})
	if err != nil {
		zap.L().Error("error when sending event to Posthog", zap.Error(err))
	}
}

func (p *PosthogClient) GetPackageToPosthogProperties(header *http.Header) posthog.Properties {
	properties := posthog.NewProperties().
		Set("browser", header.Get("browser")).
		Set("lang", header.Get("lang")).
		Set("lang_version", header.Get("lang_version")).
		Set("machine", header.Get("machine")).
		Set("os", header.Get("os")).
		Set("package_version", header.Get("package_version")).
		Set("processor", header.Get("processor")).
		Set("publisher", header.Get("publisher")).
		Set("release", header.Get("release")).
		Set("sdk_runtime", header.Get("sdk_runtime")).
		Set("system", header.Get("system"))

	return properties
}
