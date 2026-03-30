package grpc

import (
	"context"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	channelzpb "google.golang.org/grpc/channelz/grpc_channelz_v1"
	channelzservice "google.golang.org/grpc/channelz/service"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

const (
	channelzPollInterval = 10 * time.Second
	channelzMaxResults   = 200

	channelzClientTypeGCS           = "gcs"
	channelzClientTypeOTELCollector = "otel-collector"
	channelzClientTypeUnknown       = "unknown"
)

var (
	channelzInitOnce sync.Once
	channelzCountsMu sync.RWMutex

	channelzTargetClientTypes sync.Map

	channelzCounts = map[string]stateCounts{}

	otelCollectorTarget = normalizeChannelzTarget(os.Getenv("OTEL_COLLECTOR_GRPC_ENDPOINT"))
)

type stateCounts struct {
	idle             int64
	connecting       int64
	ready            int64
	transientFailure int64
	shutdown         int64
	unknown          int64
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

	channelzTargetClientTypes.Store(target, clientType)
}

func StartChannelzSampler(ctx context.Context) {
	channelzInitOnce.Do(func() {
		initChannelzCounts()

		meter := otel.Meter("github.com/e2b-dev/infra/packages/shared/pkg/grpc")
		gauge, err := meter.Int64ObservableGauge(
			"grpc.client.connections",
			metric.WithDescription("Current number of gRPC client connections by connectivity state"),
			metric.WithUnit("{connection}"),
		)
		if err != nil {
			logger.L().Warn(ctx, "failed to initialize gRPC channelz metric", zap.Error(err))

			return
		}

		_, err = meter.RegisterCallback(func(_ context.Context, observer metric.Observer) error {
			channelzCountsMu.RLock()
			defer channelzCountsMu.RUnlock()

			for clientType, counts := range channelzCounts {
				observer.ObserveInt64(gauge, counts.idle, metric.WithAttributes(
					attribute.String("grpc.connectivity.state", "IDLE"),
					attribute.String("grpc.client.type", clientType),
				))
				observer.ObserveInt64(gauge, counts.connecting, metric.WithAttributes(
					attribute.String("grpc.connectivity.state", "CONNECTING"),
					attribute.String("grpc.client.type", clientType),
				))
				observer.ObserveInt64(gauge, counts.ready, metric.WithAttributes(
					attribute.String("grpc.connectivity.state", "READY"),
					attribute.String("grpc.client.type", clientType),
				))
				observer.ObserveInt64(gauge, counts.transientFailure, metric.WithAttributes(
					attribute.String("grpc.connectivity.state", "TRANSIENT_FAILURE"),
					attribute.String("grpc.client.type", clientType),
				))
				observer.ObserveInt64(gauge, counts.shutdown, metric.WithAttributes(
					attribute.String("grpc.connectivity.state", "SHUTDOWN"),
					attribute.String("grpc.client.type", clientType),
				))
				observer.ObserveInt64(gauge, counts.unknown, metric.WithAttributes(
					attribute.String("grpc.connectivity.state", "UNKNOWN"),
					attribute.String("grpc.client.type", clientType),
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

			for {
				sampleChannelzConnections(ctx, client)

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

func sampleChannelzConnections(ctx context.Context, client channelzpb.ChannelzClient) {
	countsByType := map[string]map[channelzpb.ChannelConnectivityState_State]int64{}
	for _, clientType := range registeredChannelzClientTypes() {
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
			target := ch.GetData().GetTarget()
			if isInProcessChannelzTarget(target) {
				continue
			}

			clientType := detectChannelzClientType(target)
			if _, ok := countsByType[clientType]; !ok {
				countsByType[clientType] = map[channelzpb.ChannelConnectivityState_State]int64{}
			}

			subRefs := ch.GetSubchannelRef()
			if len(subRefs) == 0 {
				state := channelzpb.ChannelConnectivityState_UNKNOWN
				if data := ch.GetData(); data != nil && data.GetState() != nil {
					state = data.GetState().GetState()
				}

				countsByType[clientType][state]++
			} else {
				for _, subRef := range subRefs {
					subResp, err := client.GetSubchannel(ctx, &channelzpb.GetSubchannelRequest{SubchannelId: subRef.GetSubchannelId()})
					if err != nil {
						continue
					}

					state := subResp.GetSubchannel().GetData().GetState().GetState()
					countsByType[clientType][state]++
				}
			}

			if ch.GetRef().GetChannelId() >= startID {
				startID = ch.GetRef().GetChannelId() + 1
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

	channelzCountsMu.Lock()
	channelzCounts = newCounts
	channelzCountsMu.Unlock()
}

func initChannelzCounts() {
	channelzCountsMu.Lock()
	defer channelzCountsMu.Unlock()

	channelzCounts = map[string]stateCounts{}
	for _, clientType := range registeredChannelzClientTypes() {
		channelzCounts[clientType] = stateCounts{}
	}
}

func registeredChannelzClientTypes() []string {
	unique := map[string]struct{}{}

	channelzTargetClientTypes.Range(func(_, value any) bool {
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

func isInProcessChannelzTarget(target string) bool {
	target = normalizeChannelzTarget(target)

	return target == "bufnet"
}

func detectChannelzClientType(target string) string {
	normalizedTarget := normalizeChannelzTarget(target)

	if clientType, ok := lookupRegisteredChannelzClientType(normalizedTarget); ok {
		return clientType
	}

	if isGCSRelatedTarget(normalizedTarget) {
		return channelzClientTypeGCS
	}

	if isOTELCollectorTarget(normalizedTarget) {
		return channelzClientTypeOTELCollector
	}

	return channelzClientTypeUnknown
}

func isGCSRelatedTarget(target string) bool {
	if target == "" {
		return false
	}

	if strings.Contains(target, "storage.googleapis.com") {
		return true
	}

	// GCS gRPC stack can open helper channels for DirectPath/xDS/RLS control
	// plane. They are part of GCS connectivity and are most useful grouped with
	// GCS instead of shown as unknown.
	return strings.Contains(target, "googleapis.com") ||
		strings.Contains(target, "google-c2p") ||
		strings.Contains(target, "traffic-director") ||
		strings.Contains(target, "rls")
}

func lookupRegisteredChannelzClientType(normalizedTarget string) (string, bool) {
	if normalizedTarget == "" {
		return "", false
	}

	value, ok := channelzTargetClientTypes.Load(normalizedTarget)
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

func isOTELCollectorTarget(target string) bool {
	if target == "" {
		return false
	}

	if otelCollectorTarget != "" && target == otelCollectorTarget {
		return true
	}

	return strings.Contains(target, "localhost:4317") ||
		strings.Contains(target, "127.0.0.1:4317") ||
		strings.Contains(target, "otel-collector")
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
