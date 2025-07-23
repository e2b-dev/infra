package edgepassthrough

import (
	"context"
	"fmt"
	"io"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/e2b-dev/infra/packages/proxy/internal/edge/authorization"
	e2binfo "github.com/e2b-dev/infra/packages/proxy/internal/edge/info"
	e2borchestrators "github.com/e2b-dev/infra/packages/proxy/internal/edge/pool"
	"github.com/e2b-dev/infra/packages/proxy/internal/edge/sandboxes"
	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	"github.com/e2b-dev/infra/packages/shared/pkg/edge"
	api "github.com/e2b-dev/infra/packages/shared/pkg/http/edge"
)

type NodePassThroughServer struct {
	nodes   *e2borchestrators.OrchestratorsPool
	catalog sandboxes.SandboxesCatalog

	info   *e2binfo.ServiceInfo
	server *grpc.Server

	ctx           context.Context
	authorization authorization.AuthorizationService
}

const (
	grpcMaxMsgSize       = 5 * 1024 * 1024 // 5 MiB
	grpcHealthMethodName = "/EdgePassThrough/healthcheck"
)

var clientStreamDescForProxying = &grpc.StreamDesc{ServerStreams: true, ClientStreams: true}

func NewNodePassThroughServer(
	ctx context.Context,
	nodes *e2borchestrators.OrchestratorsPool,
	info *e2binfo.ServiceInfo,
	authorization authorization.AuthorizationService,
	catalog sandboxes.SandboxesCatalog,
) *grpc.Server {
	nodePassThrough := &NodePassThroughServer{
		authorization: authorization,
		nodes:         nodes,
		catalog:       catalog,
		info:          info,
		ctx:           ctx,
	}

	return grpc.NewServer(
		grpc.UnknownServiceHandler(nodePassThrough.handler),
		grpc.MaxRecvMsgSize(grpcMaxMsgSize),
	)
}

func (s *NodePassThroughServer) director(ctx context.Context) (*grpc.ClientConn, metadata.MD, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return nil, nil, status.Error(codes.InvalidArgument, "error getting metadata from context")
	}

	auths := md.Get(consts.EdgeRpcAuthHeader)
	if len(auths) == 0 || len(auths) > 1 {
		return nil, nil, status.Error(codes.Unauthenticated, "error getting authentication metadata from context")
	}

	// Verify authorization header
	auth := auths[0]
	err := s.authorization.VerifyAuthorization(auth)
	if err != nil {
		return nil, nil, status.Error(codes.PermissionDenied, err.Error())
	}

	serviceInstanceIDs := md.Get(consts.EdgeRpcServiceInstanceIDHeader)
	if len(serviceInstanceIDs) == 0 || len(serviceInstanceIDs) > 1 {
		return nil, nil, status.Error(codes.InvalidArgument, "service instance id header missing or invalid")
	}

	serviceInstanceID := serviceInstanceIDs[0]
	serviceInstance, ok := s.nodes.GetOrchestrator(serviceInstanceID)
	if !ok {
		return nil, nil, status.Error(codes.NotFound, "service instance not found")
	}

	return serviceInstance.GetClient().Connection, md, nil
}

// Handler - following code implement a gRPC pass-through proxy that forwards requests to the appropriate node
// Code is based on following source: https://github.com/siderolabs/grpc-proxy/tree/main
//
// Core implementation is just following methods that are handling forwarding, proper closing and propagating of errors from both sides of the stream.
// The handler is called for every request that is not handled by any other gRPC service.
func (s *NodePassThroughServer) handler(srv interface{}, serverStream grpc.ServerStream) error {
	fullMethodName, ok := grpc.MethodFromServerStream(serverStream)
	if !ok {
		return status.Errorf(codes.Internal, "low lever server stream not exists in context")
	}

	// AWS ALB health check does not allow us to do health check on different HTTP protocol that
	// on that that is used for actually proxying. So we cannot use edge API for health check as in other cases.
	if fullMethodName == grpcHealthMethodName {
		// we don't want to directly return error when service is marked as unhealthy
		//  state should be managed by load balancer that will stop sending requests to this service
		if s.info.GetStatus() != api.Unhealthy {
			return status.Error(codes.OK, "healthy")
		}

		return status.Error(codes.Unavailable, "unhealthy")
	}

	// We require that the director's returned context inherits from the serverStream.Context().
	clientConnection, md, err := s.director(serverStream.Context())
	if err != nil {
		return err
	}

	clientCtx, clientCancel := context.WithCancel(s.ctx)
	defer clientCancel()

	callback, err := s.eventsHandler(md)
	if err != nil {
		return status.Errorf(codes.Internal, "failed to handle events: %v", err)
	}

	var returnErr error
	if callback != nil {
		defer callback(returnErr)
	}

	clientStream, err := grpc.NewClientStream(clientCtx, clientStreamDescForProxying, clientConnection, fullMethodName)
	if err != nil {
		return err
	}

	// Explicitly *do not close* s2cErrChan and c2sErrChan, otherwise the select below will not terminate.
	// Channels do not have to be closed, it is just a control flow mechanism, see
	// https://groups.google.com/forum/#!msg/golang-nuts/pZwdYRGxCIk/qpbHxRRPJdUJ
	s2cErrChan := s.forwardServerToClient(serverStream, clientStream)
	c2sErrChan := s.forwardClientToServer(clientStream, serverStream)

	// We don't know which side is going to stop sending first, so we need a select between the two.
	for i := 0; i < 2; i++ {
		select {
		case s2cErr := <-s2cErrChan:
			if s2cErr == io.EOF {
				// this is the happy case where the sender has encountered io.EOF, and won't be sending anymore./
				// the clientStream>serverStream may continue pumping though.
				clientStream.CloseSend()
			} else {
				// however, we may have gotten a receive error (stream disconnected, a read error etc) in which case we need
				// to cancel the clientStream to the backend, let all of its goroutines be freed up by the CancelFunc and
				// exit with an error to the stack
				clientCancel()
				returnErr = s2cErr
				return status.Errorf(codes.Internal, "failed proxying s2c: %v", s2cErr)
			}
		case c2sErr := <-c2sErrChan:
			// This happens when the clientStream has nothing else to offer (io.EOF), returned a gRPC error. In those two
			// cases we may have received Trailers as part of the call. In case of other errors (stream closed) the trailers
			// will be nil.
			serverStream.SetTrailer(clientStream.Trailer())
			// c2sErr will contain RPC error from client code. If not io.EOF return the RPC error as server stream error.
			if c2sErr != io.EOF {
				returnErr = c2sErr
				return c2sErr
			}
			return nil
		}
	}

	return status.Errorf(codes.Internal, "gRPC proxying should never reach this stage.")
}

func (s *NodePassThroughServer) forwardClientToServer(src grpc.ClientStream, dst grpc.ServerStream) chan error {
	ret := make(chan error, 1)

	go func() {
		md, err := src.Header()
		if err != nil {
			ret <- err
			return
		}

		if err := dst.SendHeader(md); err != nil {
			ret <- err
			return
		}

		f := &emptypb.Empty{}
		for {
			if err := src.RecvMsg(f); err != nil {
				ret <- err // this can be io.EOF which is happy case
				break
			}

			if err := dst.SendMsg(f); err != nil {
				ret <- err
				break
			}
		}
	}()

	return ret
}

func (s *NodePassThroughServer) forwardServerToClient(src grpc.ServerStream, dst grpc.ClientStream) chan error {
	ret := make(chan error, 1)

	go func() {
		f := &emptypb.Empty{}
		for {
			if err := src.RecvMsg(f); err != nil {
				ret <- err // this can be io.EOF which is happy case
				break
			}
			if err := dst.SendMsg(f); err != nil {
				ret <- err
				break
			}
		}
	}()

	return ret
}

func (s *NodePassThroughServer) eventsHandler(md metadata.MD) (func(error), error) {
	eventTypeHeaders := md.Get(edge.EventTypeHeader)
	if len(eventTypeHeaders) == 0 {
		return nil, nil
	}

	eventType := eventTypeHeaders[0]
	switch eventType {
	case edge.CatalogCreateEventType:
		return s.catalogCreateEventHandler(md)
	case edge.CatalogDeleteEventType:
		return s.catalogDeleteEventHandler(md)
	default:
		return nil, status.Errorf(codes.InvalidArgument, "event type %s is not supported", eventType)
	}
}

func (s *NodePassThroughServer) catalogCreateEventHandler(md metadata.MD) (func(error), error) {
	c, err := edge.ParseSandboxCatalogCreateEvent(md)
	if err != nil {
		return nil, err
	}

	err = s.catalog.StoreSandbox(
		c.SandboxID,
		&sandboxes.SandboxInfo{
			OrchestratorID:          c.OrchestratorID,
			ExecutionID:             c.ExecutionID,
			SandboxStartedAt:        c.SandboxStartTime,
			SandboxMaxLengthInHours: c.SandboxMaxLengthInHours,
		},
		time.Duration(c.SandboxMaxLengthInHours)*time.Hour,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to store sandbox in catalog: %w", err)
	}

	return func(err error) {
		if err == nil {
			return
		}

		deleteErr := s.catalog.DeleteSandbox(c.SandboxID, c.ExecutionID)
		if deleteErr != nil {
			zap.L().Error("Failed to delete sandbox in catalog after failing request", zap.Error(deleteErr))
		}
	}, nil
}

func (s *NodePassThroughServer) catalogDeleteEventHandler(md metadata.MD) (func(error), error) {
	d, err := edge.ParseSandboxCatalogDeleteEvent(md)
	if err != nil {
		return nil, err
	}

	err = s.catalog.DeleteSandbox(d.SandboxID, d.ExecutionID)
	if err != nil {
		return nil, fmt.Errorf("failed to delete sandbox from catalog: %w", err)
	}

	return nil, nil
}
