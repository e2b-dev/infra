package constants

import (
	"os"

	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
)

var ClientID = os.Getenv("NODE_ID")[:consts.NodeIDLength]
