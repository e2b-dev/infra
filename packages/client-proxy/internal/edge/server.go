package edge

import (
	"context"
	"errors"
	edge "github.com/e2b-dev/infra/packages/shared/pkg/grpc/edge"
	"github.com/go-redsync/redsync/v4"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

type edgeServer struct {
	edge.UnimplementedEdgeServiceServer

	healthy     bool
	redisClient *redis.Client
	mx          *redsync.Mutex
}

func (e *edgeServer) Hello(ctx context.Context, _ *edge.HelloRequest) (*edge.HelloResponse, error) {
	err := e.mx.Lock()
	if err != nil {
		zap.L().Error("failed to acquire distributed mutex", zap.Error(err))
		return nil, errors.New("failed to acquire distributed mutex")
	}

	defer e.mx.Unlock()

	keys := e.redisClient.Keys(ctx, "*")
	if err := keys.Err(); err != nil {
		zap.L().Error("failed to get keys from redis", zap.Error(err))
		return nil, err
	}

	for _, key := range keys.Val() {
		println(key)
	}

	return &edge.HelloResponse{Message: "Hello World!"}, nil
}
