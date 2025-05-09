package service_discovery

import (
	"context"
	"net"
	"sync"
	"time"

	"go.uber.org/zap"
)

type DnsServiceDiscovery struct {
	serviceId      string
	serviceType    string
	serviceStatus  string
	serviceVersion string

	nodeIp   string
	nodePort int

	logger *zap.Logger
	lock   sync.Mutex

	orchestratorsDomain string
	orchestratorsPort   int
}

type DnsServiceDiscoveryConfig struct {
	Logger *zap.Logger

	NodeIp   string
	NodePort int

	ServiceId      string
	ServiceType    string
	ServiceVersion string
	ServiceStatus  string

	OrchestratorsDomain string
	OrchestratorsPort   int
}

const (
	dnsMaxRetries = 3
)

func NewDnsServiceDiscovery(config *DnsServiceDiscoveryConfig) *DnsServiceDiscovery {
	return &DnsServiceDiscovery{
		nodeIp:   config.NodeIp,
		nodePort: config.NodePort,

		serviceId:      config.ServiceId,
		serviceStatus:  config.ServiceStatus, // just initial status
		serviceVersion: config.ServiceVersion,
		serviceType:    config.ServiceType,

		orchestratorsDomain: config.OrchestratorsDomain,
		orchestratorsPort:   config.OrchestratorsPort,

		logger: config.Logger,
	}
}

func (sd *DnsServiceDiscovery) ListNodes(_ context.Context) (map[string]*ServiceDiscoveryItem, error) {
	return sd.getNodes()
}

func (sd *DnsServiceDiscovery) GetNodeById(_ context.Context, nodeId string) (*ServiceDiscoveryItem, error) {
	nodes, err := sd.getNodes()
	if err != nil {
		return nil, err
	}

	for id, node := range nodes {
		if id == nodeId {
			return node, nil
		}
	}

	return nil, NodeNotFoundErr
}

func (sd *DnsServiceDiscovery) GetSelfNodeId() string {
	return sd.serviceId
}

func (sd *DnsServiceDiscovery) SetSelfStatus(status string) {
	sd.serviceStatus = status
}

func (sd *DnsServiceDiscovery) getNodes() (map[string]*ServiceDiscoveryItem, error) {
	sd.lock.Lock()
	defer sd.lock.Unlock()

	nodes := make(map[string]*ServiceDiscoveryItem)

	for _ = range dnsMaxRetries {
		ips, err := net.LookupIP(sd.orchestratorsDomain)
		if err != nil {
			sd.logger.Error("DNS service discovery failed", zap.Error(err))
			time.Sleep(5 * time.Millisecond)
			continue
		}

		sd.logger.Debug("DNS service discovery response", zap.Int("ips", len(ips)))

		for _, answer := range ips {
			orchestratorIp := answer.String()

			// we want to use orchestrator ip as static service id
			nodes[orchestratorIp] = &ServiceDiscoveryItem{
				SchemaVersion: schemaVersion,

				ServiceType:    ServiceTypeOrchestrator,
				ServiceVersion: sd.serviceVersion, // mocked
				NodeIp:         orchestratorIp,
				NodePort:       sd.orchestratorsPort,

				Status:       StatusHealthy,
				RegisteredAt: time.Now().Unix(),                               // mocked
				ExpiresAt:    time.Now().Add(defaultServiceExpiration).Unix(), // mocked
			}
		}

		break
	}

	// inject itself
	nodes[sd.serviceId] = &ServiceDiscoveryItem{
		SchemaVersion: schemaVersion,

		ServiceType:    ServiceTypeEdge,
		ServiceVersion: sd.serviceVersion,

		NodeIp:   sd.nodeIp,
		NodePort: sd.nodePort,

		Status:       sd.serviceStatus,
		RegisteredAt: time.Now().Unix(),
		ExpiresAt:    time.Now().Add(defaultServiceExpiration).Unix(),
	}

	return nodes, nil
}
