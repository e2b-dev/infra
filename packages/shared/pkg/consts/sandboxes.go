package consts

import (
	"strconv"

	"github.com/e2b-dev/infra/packages/shared/pkg/env"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

const NodeIDLength = 8

// ClientID Sandbox ID client part used during migration when we are still returning client but its no longer serving its purpose,
// and we are returning it only for backward compatibility with SDK clients.
// We don't want to use some obviously dummy value such as empty zeros, because for users it will look like something is wrong with the sandbox id
const ClientID = "6532622b"

var OrchestratorAPIPort = uint16(utils.Must(strconv.ParseUint(env.GetEnv("ORCHESTRATOR_PORT", "5008"), 10, 16)))
