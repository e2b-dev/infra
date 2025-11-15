package consts

import "os"

const NodeIDLength = 8

// ClientID Sandbox ID client part used during migration when we are still returning client but its no longer serving its purpose,
// and we are returning it only for backward compatibility with SDK clients.
// We don't want to use some obviously dummy value such as empty zeros, because for users it will look like something is wrong with the sandbox id
const ClientID = "6532622b"

var OrchestratorPort = os.Getenv("ORCHESTRATOR_PORT")
