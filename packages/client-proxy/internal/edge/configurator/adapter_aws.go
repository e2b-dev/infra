package configuration

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/aws/aws-sdk-go/aws/arn"
	"io/ioutil"
	"net/http"
)

// todo: we should add version and hash here
type secretStoreConfig struct {
	SelfUpdateSourceUrl string `json:"self_update_source_url"`

	ServicePort int `json:"service_port"`

	ApiSecret   string `json:"api_secret"`
	ApiEndpoint string `json:"api_endpoint"`

	RedisHost       string `json:"redis_host"`
	RedisReaderHost string `json:"redis_read_host"`
}

type AwsAdapter struct {
	secretsManagerItemArn string
}

func NewAdapterWithInstanceMetadata() (*AwsAdapter, error) {
	secretArn, err := getArnFromInstanceMetadata()
	if err != nil {
		return nil, fmt.Errorf("failed to get ARN from instance metadata: %w", err)
	}

	return NewAwsAdapter(secretArn)
}

func NewAwsAdapter(secretsManagerItemArn string) (*AwsAdapter, error) {
	return &AwsAdapter{secretsManagerItemArn: secretsManagerItemArn}, nil
}

func (a *AwsAdapter) GetConfiguration(ctx context.Context) (*Config, error) {
	secretArn, err := arn.Parse(a.secretsManagerItemArn)
	if err != nil {
		return nil, fmt.Errorf("failed to parse ARN: %w", err)
	}

	// adjust the region to match the ARN of secret store
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(secretArn.Region))
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	svc := secretsmanager.NewFromConfig(cfg)
	resp, err := svc.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{SecretId: aws.String(a.secretsManagerItemArn)})
	if err != nil {
		return nil, fmt.Errorf("failed to get secret value: %w", err)
	}

	if resp.SecretString == nil {
		return nil, fmt.Errorf("secret string is nil")
	}

	conf := &secretStoreConfig{}
	err = json.Unmarshal([]byte(*resp.SecretString), conf)
	if err != nil {
		return nil, fmt.Errorf("failed to parse secret value: %w", err)
	}

	return &Config{
		// todo
		RedisUrl:       conf.RedisHost,
		RedisReaderUrl: conf.RedisReaderHost,

		//RedisUrl:       "redis://localhost:6379",
		//RedisReaderUrl: "redis://localhost:6379",

		ServicePort: conf.ServicePort,

		ApiUrl:    conf.ApiEndpoint,
		ApiSecret: conf.ApiSecret,

		SelfUpdateSourceUrl:    &conf.SelfUpdateSourceUrl,
		SelfUpdateAutoInterval: 10,
		SelfUpdateAutoEnabled:  true,
	}, nil
}

func getArnFromInstanceMetadata() (string, error) {
	// ec2 internal aws metadata URL for instance tags
	const metadataURL = "http://169.254.169.254/latest/meta-data/tags/instance/config_storage_arn"
	resp, err := http.Get(metadataURL)
	if err != nil {
		return "", fmt.Errorf("failed to get metadata: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("metadata request failed: %s", resp.Status)
	}

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read metadata response: %w", err)
	}

	return string(body), nil
}
