package server

import (
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	orchestratormocks "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator/mocks"
)

func TestGetBuildFrame_RejectsNegativeRange(t *testing.T) {
	t.Parallel()

	stream := orchestratormocks.NewMockChunkService_GetBuildFrameServer(t)
	stream.EXPECT().Context().Return(t.Context())

	s := &Server{}
	err := s.GetBuildFrame(&orchestrator.GetBuildFrameRequest{
		BuildId:  "build-1",
		FileName: "memfile",
		Offset:   -1,
		Length:   1,
	}, stream)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.InvalidArgument, st.Code())
}
