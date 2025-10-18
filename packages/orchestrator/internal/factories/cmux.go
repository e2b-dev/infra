package factories

import (
	"context"
	"fmt"
	"net"

	"github.com/soheilhy/cmux"
)

func NewCMUXServer(ctx context.Context, port uint16) (cmux.CMux, error) {
	var lisCfg net.ListenConfig
	lis, err := lisCfg.Listen(ctx, "tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return nil, fmt.Errorf("failed to listen on port %d: %w", port, err)
	}

	m := cmux.New(lis)

	return m, nil
}
