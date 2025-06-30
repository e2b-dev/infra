package service_discovery

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
)

type AwsEc2ServiceDiscovery struct {
	logger  *zap.Logger
	entries *smap.Map[*ServiceDiscoveryItem]
	client  *ec2.Client

	port    int
	filters []types.Filter
}

const (
	ec2QueryRefreshInterval = 10 * time.Second
)

func NewAwsEc2ServiceDiscovery(ctx context.Context, region string, filterTagsKeys []string, port int, logger *zap.Logger) (*AwsEc2ServiceDiscovery, error) {
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	client := ec2.NewFromConfig(cfg)

	filters := make([]types.Filter, 0)
	filters = append(
		filters,
		types.Filter{
			Name:   aws.String("tag-key"),
			Values: filterTagsKeys,
		},
	)

	sd := &AwsEc2ServiceDiscovery{
		client:  client,
		logger:  logger,
		port:    port,
		filters: filters,

		entries: smap.New[*ServiceDiscoveryItem](),
	}

	go func() { sd.keepInSync(ctx) }()

	return sd, nil
}

func (sd *AwsEc2ServiceDiscovery) ListNodes(_ context.Context) ([]*ServiceDiscoveryItem, error) {
	entries := sd.entries.Items()
	items := make([]*ServiceDiscoveryItem, 0)

	for _, item := range entries {
		items = append(items, item)
	}

	return items, nil
}

func (sd *AwsEc2ServiceDiscovery) keepInSync(ctx context.Context) {
	// Run the first sync immediately
	sd.sync(ctx)

	ticker := time.NewTicker(ec2QueryRefreshInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			sd.logger.Info("Stopping service discovery keep-in-sync")
			return
		case <-ticker.C:
			sd.sync(ctx)
		}
	}
}

func (sd *AwsEc2ServiceDiscovery) sync(ctx context.Context) {
	ctxTimeout, ctxCancel := context.WithTimeout(ctx, ec2QueryRefreshInterval)
	defer ctxCancel()

	select {
	case <-ctxTimeout.Done():
		sd.logger.Info("Service discovery sync timed out")
		return
	default:
		input := &ec2.DescribeInstancesInput{Filters: sd.filters}

		instancesHosts := make(map[string]string)
		instances, err := sd.client.DescribeInstances(ctx, input)
		if err != nil {
			sd.logger.Error("Failed to describe instances", zap.Error(err))
			return
		}

		// Create or update the entries
		for _, reservation := range instances.Reservations {
			for _, instance := range reservation.Instances {
				// Filter only running instances
				if instance.State.Name != types.InstanceStateNameRunning {
					continue
				}

				ip := *instance.PrivateIpAddress
				key := fmt.Sprintf("%s:%d", ip, sd.port)

				sd.entries.Insert(
					key, &ServiceDiscoveryItem{NodeIP: ip, NodePort: sd.port},
				)

				instancesHosts[key] = key
			}
		}

		// Remove entries that are no longer in EC2 response
		for key := range sd.entries.Items() {
			if _, ok := instancesHosts[key]; !ok {
				sd.entries.Remove(key)
			}
		}
	}
}
