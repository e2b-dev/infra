package consts

import "os"

const NodeIDLength = 8

// ClientID Sandbox ID client part used during migration when we are still returning client but its no longer serving its purpose,
// and we are returning it only for backward compatibility with SDK clients.
const ClientID = "fdf09568"

var OrchestratorPort = os.Getenv("ORCHESTRATOR_PORT")
