package grpc

import (
	"context"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	channelzpb "google.golang.org/grpc/channelz/grpc_channelz_v1"
	channelzservice "google.golang.org/grpc/channelz/service"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const (
	channelzPollInterval = 15 * time.Second
	// channelzMaxResults controls page size for channelz GetTopChannels requests
	// (maximum top-level channels returned per RPC call).
	channelzMaxResults = 200

	channelzClientTypeGCS           = "gcs"
	channelzClientTypeGCSControl    = "gcs-control-plane"
	channelzClientTypeArtifactReg   = "artifact-registry"
	channelzClientTypeOTELCollector = "otel-collector"
	channelzClientTypeUnknown       = "unknown"

	labelGRPCClientType        = "grpc.client.type"
	labelGRPCConnectivityState = "grpc.connectivity.state"
)

var (
	channelzInitOnce sync.Once
	channelzState    = newChannelzState()

	otelCollectorTarget = normalizeChannelzTarget(telemetry.OTELCollectorGRPCEndpoint())
)

type stateCounts struct {
	idle             int64
	connecting       int64
	ready            int64
	transientFailure int64
	shutdown         int64
	unknown          int64
}

type channelzSamplerState struct {
	mu                sync.RWMutex
	counts            map[string]stateCounts
	targetClientTypes sync.Map
	unknownTargets    sync.Map
}

func newChannelzState() *channelzSamplerState {
	return &channelzSamplerState{
		counts: map[string]stateCounts{},
	}
}

// registerTargetClientType stores a single clientType per normalised target.
// If different logical clients dial the same address (e.g. "orchestrator" and
// "cluster-orchestrator" sharing an endpoint), the last registration wins and
// earlier ones are lost. This doesn't happen today because each client type
// connects to distinct addresses, but if that changes the mapping would need to
// support multiple client types per target.
func (s *channelzSamplerState) registerTargetClientType(target, clientType string) {
	s.targetClientTypes.Store(target, clientType)
}

func (s *channelzSamplerState) lookupRegisteredClientType(normalizedTarget string) (string, bool) {
	if normalizedTarget == "" {
		return "", false
	}

	value, ok := s.targetClientTypes.Load(normalizedTarget)
	if !ok {
		return "", false
	}

	clientType, ok := value.(string)
	if !ok {
		return "", false
	}

	clientType = strings.TrimSpace(clientType)
	if clientType == "" {
		return "", false
	}

	return clientType, true
}

func (s *channelzSamplerState) registeredClientTypes() []string {
	unique := map[string]struct{}{}

	s.targetClientTypes.Range(func(_, value any) bool {
		label, ok := value.(string)
		if !ok {
			return true
		}

		label = strings.TrimSpace(label)
		if label == "" {
			return true
		}

		unique[label] = struct{}{}

		return true
	})

	clientTypes := make([]string, 0, len(unique))
	for label := range unique {
		clientTypes = append(clientTypes, label)
	}

	return clientTypes
}

func (s *channelzSamplerState) logUnknownTargetOnce(ctx context.Context, target string) {
	normalizedTarget := normalizeChannelzTarget(target)
	if normalizedTarget == "" {
		return
	}

	if _, loaded := s.unknownTargets.LoadOrStore(normalizedTarget, struct{}{}); loaded {
		return
	}

	logger.L().Info(ctx, "unknown gRPC channelz target", zap.String("target", normalizedTarget))
}

func RegisterChannelzTarget(conn *grpc.ClientConn, clientType string) {
	if conn == nil {
		return
	}

	clientType = strings.TrimSpace(clientType)
	if clientType == "" {
		return
	}

	target := normalizeChannelzTarget(conn.Target())
	if target == "" {
		return
	}

	channelzState.registerTargetClientType(target, clientType)
}

func StartChannelzSampler(ctx context.Context) {
	channelzInitOnce.Do(func() {
		gauge, err := meter.Int64ObservableGauge(
			"grpc.client.connections",
			metric.WithDescription("Current number of gRPC client connections by connectivity state"),
			metric.WithUnit("{connection}"),
		)
		if err != nil {
			logger.L().Warn(ctx, "failed to initialize gRPC channelz metric", zap.Error(err))

			return
		}

		reg, err := meter.RegisterCallback(func(_ context.Context, observer metric.Observer) error {
			channelzState.mu.RLock()
			defer channelzState.mu.RUnlock()

			for clientType, counts := range channelzState.counts {
				observer.ObserveInt64(gauge, counts.idle, metric.WithAttributes(
					attribute.String(labelGRPCConnectivityState, "IDLE"),
					attribute.String(labelGRPCClientType, clientType),
				))
				observer.ObserveInt64(gauge, counts.connecting, metric.WithAttributes(
					attribute.String(labelGRPCConnectivityState, "CONNECTING"),
					attribute.String(labelGRPCClientType, clientType),
				))
				observer.ObserveInt64(gauge, counts.ready, metric.WithAttributes(
					attribute.String(labelGRPCConnectivityState, "READY"),
					attribute.String(labelGRPCClientType, clientType),
				))
				observer.ObserveInt64(gauge, counts.transientFailure, metric.WithAttributes(
					attribute.String(labelGRPCConnectivityState, "TRANSIENT_FAILURE"),
					attribute.String(labelGRPCClientType, clientType),
				))
				observer.ObserveInt64(gauge, counts.shutdown, metric.WithAttributes(
					attribute.String(labelGRPCConnectivityState, "SHUTDOWN"),
					attribute.String(labelGRPCClientType, clientType),
				))
				observer.ObserveInt64(gauge, counts.unknown, metric.WithAttributes(
					attribute.String(labelGRPCConnectivityState, "UNKNOWN"),
					attribute.String(labelGRPCClientType, clientType),
				))
			}

			return nil
		}, gauge)
		if err != nil {
			logger.L().Warn(ctx, "failed to register gRPC channelz metric callback", zap.Error(err))

			return
		}

		client, closeFn, err := createInProcessChannelzClient()
		if err != nil {
			logger.L().Warn(ctx, "failed to initialize channelz client", zap.Error(err))

			return
		}

		go func() {
			ticker := time.NewTicker(channelzPollInterval)
			defer ticker.Stop()
			defer closeFn()
			defer func() {
				err := reg.Unregister()
				if err != nil {
					logger.L().Warn(ctx, "failed to unregister gRPC channelz metric callback", zap.Error(err))
				}
			}()

			for {
				sampleChannelzConnections(ctx, client, channelzState)

				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
				}
			}
		}()
	})
}

func createInProcessChannelzClient() (channelzpb.ChannelzClient, func(), error) {
	lis := bufconn.Listen(1024 * 1024)
	srv := grpc.NewServer()
	channelzservice.RegisterChannelzServiceToServer(srv)

	go func() {
		_ = srv.Serve(lis)
	}()

	dialer := func(context.Context, string) (net.Conn, error) {
		return lis.Dial()
	}

	conn, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(dialer),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		srv.Stop()
		_ = lis.Close()

		return nil, nil, err
	}

	closeFn := func() {
		_ = conn.Close()
		srv.Stop()
		_ = lis.Close()
	}

	return channelzpb.NewChannelzClient(conn), closeFn, nil
}

func sampleChannelzConnections(ctx context.Context, client channelzpb.ChannelzClient, samplerState *channelzSamplerState) {
	countsByType := map[string]map[channelzpb.ChannelConnectivityState_State]int64{}
	for _, clientType := range samplerState.registeredClientTypes() {
		countsByType[clientType] = map[channelzpb.ChannelConnectivityState_State]int64{}
	}

	var startID int64

	for {
		resp, err := client.GetTopChannels(ctx, &channelzpb.GetTopChannelsRequest{
			StartChannelId: startID,
			MaxResults:     channelzMaxResults,
		})
		if err != nil {
			logger.L().Warn(ctx, "failed to sample gRPC channelz connections", zap.Error(err))

			return
		}

		for _, ch := range resp.GetChannel() {
			if ch.GetRef().GetChannelId() >= startID {
				startID = ch.GetRef().GetChannelId() + 1
			}

			target := ch.GetData().GetTarget()
			if shouldSkipChannelzTarget(target) {
				continue
			}

			clientType := detectChannelzClientType(samplerState, target)
			if clientType == channelzClientTypeUnknown {
				samplerState.logUnknownTargetOnce(ctx, target)
			}

			if _, ok := countsByType[clientType]; !ok {
				countsByType[clientType] = map[channelzpb.ChannelConnectivityState_State]int64{}
			}

			subRefs := ch.GetSubchannelRef()
			if len(subRefs) == 0 {
				connState := channelzpb.ChannelConnectivityState_UNKNOWN
				if data := ch.GetData(); data != nil && data.GetState() != nil {
					connState = data.GetState().GetState()
				}

				countsByType[clientType][connState]++
			} else {
				for _, subRef := range subRefs {
					subResp, err := client.GetSubchannel(ctx, &channelzpb.GetSubchannelRequest{SubchannelId: subRef.GetSubchannelId()})
					if err != nil {
						// Subchannel was likely deregistered between GetTopChannels
						// and this call. Skip it; self-corrects on next poll cycle.
						continue
					}

					subchannelState := subResp.GetSubchannel().GetData().GetState().GetState()
					countsByType[clientType][subchannelState]++
				}
			}
		}

		if resp.GetEnd() {
			break
		}
	}

	newCounts := map[string]stateCounts{}
	for clientType, counts := range countsByType {
		known := counts[channelzpb.ChannelConnectivityState_IDLE] +
			counts[channelzpb.ChannelConnectivityState_CONNECTING] +
			counts[channelzpb.ChannelConnectivityState_READY] +
			counts[channelzpb.ChannelConnectivityState_TRANSIENT_FAILURE] +
			counts[channelzpb.ChannelConnectivityState_SHUTDOWN]

		total := int64(0)
		for _, v := range counts {
			total += v
		}

		newCounts[clientType] = stateCounts{
			idle:             counts[channelzpb.ChannelConnectivityState_IDLE],
			connecting:       counts[channelzpb.ChannelConnectivityState_CONNECTING],
			ready:            counts[channelzpb.ChannelConnectivityState_READY],
			transientFailure: counts[channelzpb.ChannelConnectivityState_TRANSIENT_FAILURE],
			shutdown:         counts[channelzpb.ChannelConnectivityState_SHUTDOWN],
			unknown:          total - known,
		}
	}

	samplerState.mu.Lock()
	samplerState.counts = newCounts
	samplerState.mu.Unlock()
}

func isInProcessChannelzTarget(target string) bool {
	target = normalizeChannelzTarget(target)

	// The sampler talks to an in-process channelz server over bufconn using
	// `passthrough:///bufnet`; skip that self-observation channel to avoid
	// counting sampler internals as application gRPC connections.
	return target == "bufnet"
}

func shouldSkipChannelzTarget(target string) bool {
	if isInProcessChannelzTarget(target) {
		return true
	}

	host := channelzTargetHost(target)

	// Skip Cloud Monitoring exporter channels: they are telemetry side traffic
	// and not relevant for application/client pool analysis.
	return host == "monitoring.googleapis.com"
}

func detectChannelzClientType(state *channelzSamplerState, target string) string {
	normalizedTarget := normalizeChannelzTarget(target)

	// For our grpc clients we set the target name
	if clientType, ok := state.lookupRegisteredClientType(normalizedTarget); ok {
		return clientType
	}

	// For clients not controlled by us we match based on target url patterns.
	if isGCSDataTarget(normalizedTarget) {
		return channelzClientTypeGCS
	}

	if isGCSControlPlaneTarget(normalizedTarget) {
		return channelzClientTypeGCSControl
	}

	if isArtifactRegistryTarget(normalizedTarget) {
		return channelzClientTypeArtifactReg
	}

	if isOTELCollectorTarget(normalizedTarget) {
		return channelzClientTypeOTELCollector
	}

	return channelzClientTypeUnknown
}

func isGCSDataTarget(target string) bool {
	host := channelzTargetHost(target)
	if host == "" {
		return false
	}

	switch host {
	case "storage.googleapis.com",
		"storage.mtls.googleapis.com":
		return true
	}

	return false
}

func isGCSControlPlaneTarget(target string) bool {
	host := channelzTargetHost(target)
	if host == "" {
		return false
	}

	switch host {
	case "rls.googleapis.com", "trafficdirector.googleapis.com":
		return true
	}

	// Traffic Director xDS control-plane hosts.
	return strings.HasSuffix(host, ".xds.googleapis.com")
}

func isArtifactRegistryTarget(target string) bool {
	host := channelzTargetHost(target)
	if host == "" {
		return false
	}

	return host == "artifactregistry.googleapis.com" ||
		host == "artifactregistry.mtls.googleapis.com"
}

func channelzTargetHost(target string) string {
	target = normalizeChannelzTarget(target)
	if target == "" {
		return ""
	}

	if idx := strings.Index(target, "/"); idx != -1 {
		target = target[:idx]
	}

	if host, port, err := net.SplitHostPort(target); err == nil {
		if _, err := strconv.Atoi(port); err == nil {
			return strings.Trim(host, "[]")
		}
	}

	return strings.Trim(target, "[]")
}

func isOTELCollectorTarget(target string) bool {
	if target == "" {
		return false
	}

	if otelCollectorTarget != "" && target == otelCollectorTarget {
		return true
	}

	host := channelzTargetHost(target)

	return host == "localhost" || host == "127.0.0.1" || host == "otel-collector"
}

func normalizeChannelzTarget(target string) string {
	target = strings.ToLower(strings.TrimSpace(target))
	if target == "" {
		return ""
	}

	if idx := strings.Index(target, ":///"); idx != -1 {
		target = target[idx+4:]
	}

	return target
}
