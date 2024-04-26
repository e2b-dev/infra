package orchestrator

import (
	"context"
	"fmt"
	"time"

	"github.com/golang/protobuf/ptypes/empty"
	nomadapi "github.com/hashicorp/nomad/api"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"
	"google.golang.org/grpc/connectivity"

	analyticscollector "github.com/e2b-dev/infra/packages/api/internal/analytics_collector"
	"github.com/e2b-dev/infra/packages/api/internal/cache/instance"
	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	"github.com/e2b-dev/infra/packages/shared/pkg/env"
)

type Node struct {
	ID       string
	CPUUsage int64
	RamUsage int64
	Client   *GRPCClient
}

type Orchestrator struct {
	nomadClient   *nomadapi.Client
	nodes         map[string]*Node
	instanceCache *instance.InstanceCache
	analytics     *analyticscollector.Analytics
}

func New(ctx context.Context, nomadClient *nomadapi.Client, logger *zap.SugaredLogger, posthogClient *analyticscollector.PosthogClient) (*Orchestrator, error) {
	analytics, err := analyticscollector.NewAnalytics()
	if err != nil {
		logger.Errorf("Error initializing Analytics client\n: %v", err)
		panic(err)
	}

	orch := &Orchestrator{
		nomadClient: nomadClient,
		nodes:       map[string]*Node{},
		analytics:   analytics,
	}

	var initialInstances []*instance.InstanceInfo
	if env.IsLocal() {
		logger.Info("Skipping loading sandboxes, running locally")
	} else {
		initialInstances, err = orch.InitialSync(ctx)
		if err != nil {
			logger.Errorf("Error initializing Orchestrator client\n: %v", err)
			panic(err)
		}
	}

	meter := otel.GetMeterProvider().Meter("nomad")

	instancesCounter, err := meter.Int64UpDownCounter(
		"api.env.instance.running",
		metric.WithDescription(
			"Number of running instances.",
		),
		metric.WithUnit("{instance}"),
	)
	if err != nil {
		panic(err)
	}

	logger.Info("Initialized Analytics client")
	instanceCache := instance.NewCache(
		analytics.Client,
		logger,
		orch.getInsertInstanceFunction(ctx),
		orch.getDeleteInstanceFunction(ctx, posthogClient, logger),
		initialInstances,
		instancesCounter,
	)

	logger.Info("Initialized instance cache")

	orch.instanceCache = instanceCache
	return orch, nil
}

func (o *Orchestrator) Close() error {
	err := o.analytics.Close()
	if err != nil {
		return err
	}

	for _, node := range o.nodes {
		err := node.Client.Close()
		if err != nil {
			return err
		}
	}

	return nil
}

func (o *Orchestrator) GetNode(nodeID string) (*Node, error) {
	if node := o.nodes[nodeID]; node != nil {
		return node, nil
	}

	return nil, fmt.Errorf("node %s not found", nodeID)
}

func (o *Orchestrator) GetClient(nodeID string) (*GRPCClient, error) {
	node, err := o.GetNode(nodeID)
	if err != nil {
		return nil, err
	}

	return node.Client, nil
}

func (o *Orchestrator) listNomadNodes() ([]*nomadapi.NodeListStub, error) {
	nodes, _, err := o.nomadClient.Nodes().List(&nomadapi.QueryOptions{Filter: "Status == \"ready\""})
	if err != nil {
		return nil, err
	}

	return nodes, nil
}

// KeepInSync the cache with the actual instances in Orchestrator to handle instances that died.
func (o *Orchestrator) KeepInSync(ctx context.Context, logger *zap.SugaredLogger) {
	for {
		time.Sleep(instance.CacheSyncTime)
		nodes, err := o.listNomadNodes()
		if err != nil {
			logger.Errorf("Error loading nodes\n: %v", err)
			continue
		}

		for _, node := range nodes {
			// If the node is not in the list, connect to it
			_, err := o.GetNode(o.getIdFromNode(node))
			if err != nil {
				_, err := o.connectToNode(ctx, node)
				if err != nil {
					logger.Errorf("Error connecting to node\n: %v", err)
				}
			}
		}

		for _, node := range o.nodes {
			logger.Infof("Node %s: CPU: %d, RAM: %d", node.ID, node.CPUUsage, node.RamUsage)
		}

		o.instanceCache.SendAnalyticsEvent()
	}
}

// InitialSync loads already running instances from Orchestrator
func (o *Orchestrator) InitialSync(ctx context.Context) (instances []*instance.InstanceInfo, err error) {
	nodes, err := o.listNomadNodes()
	if err != nil {
		return nil, err
	}

	for _, node := range nodes {
		activeInstances, instancesErr := o.connectToNode(ctx, node)
		if instancesErr != nil {
			return nil, instancesErr
		}

		instances = append(instances, activeInstances...)
	}

	return instances, nil
}

func (o *Orchestrator) getIdFromNode(node *nomadapi.NodeListStub) string {
	return node.ID[:consts.NodeIDLength]
}

func (o *Orchestrator) connectToNode(ctx context.Context, node *nomadapi.NodeListStub) ([]*instance.InstanceInfo, error) {
	client, err := NewClient(node.Address)
	if err != nil {
		return nil, err
	}

	n := &Node{
		ID:     o.getIdFromNode(node),
		Client: client,
	}
	o.nodes[n.ID] = n
	activeInstances, instancesErr := o.getInstances(ctx, o.getIdFromNode(node))
	if instancesErr != nil {
		return nil, instancesErr
	}

	for _, sandbox := range activeInstances {
		n.RamUsage += sandbox.RamMB
		n.CPUUsage += sandbox.VCPU
	}

	stream, err := client.Sandbox.StreamClosedSandboxes(context.Background(), &empty.Empty{})
	if err != nil {
		fmt.Printf("failed to stream failed sandbox: %v\n", err)
	}

	go func() {
		for {
			sandboxID, err := stream.Recv()
			if err != nil {
				if n.Client.connection.GetState() != connectivity.Ready {
					fmt.Printf("failed to receive from stream: %v\n", err)
					break
				}
				fmt.Printf("failed to receive from stream: %v\n", err)
				continue
			}

			// Delete the instance from the cache if it exists
			o.instanceCache.Kill(sandboxID.SandboxID)
		}

		// Close the client if the stream failed
		fmt.Printf("removing node %s\n", n.ID)
		err := o.nodes[n.ID].Client.Close()
		if err != nil {
			fmt.Printf("failed to close client: %v\n", err)
		}

		delete(o.nodes, n.ID)
	}()

	return activeInstances, nil
}
