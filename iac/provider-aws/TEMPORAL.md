# Temporal Server on AWS EKS

Temporal Server deployed on EKS for fault-tolerant multi-agent orchestration. Workflows survive pod/node failures and resume exactly where they left off.

## What Was Deployed

| Component | Replicas | Purpose |
|-----------|----------|---------|
| Frontend | 2 | gRPC API gateway (port 7233) |
| History | 2 | Workflow state management |
| Matching | 2 | Task queue dispatch |
| Worker (internal) | 1 | System workflows (archival, replication) |
| Web UI | 1 | Workflow visibility dashboard (port 8080) |
| Admin Tools | 1 | `tctl` CLI for namespace/workflow management |

**Persistence**: Aurora PostgreSQL (`temporal` + `temporal_visibility` databases)
**Security**: mTLS (internode + frontend), Kubernetes NetworkPolicy
**History Shards**: 512 (IMMUTABLE after first deploy)

## Pre-Deployment Steps

Before enabling `temporal_enabled = true`, create the databases and user on Aurora:

```sql
CREATE DATABASE temporal;
CREATE DATABASE temporal_visibility;
CREATE USER temporal WITH PASSWORD '<from AWS Secrets Manager: {prefix}temporal-db-password>';
GRANT ALL PRIVILEGES ON DATABASE temporal TO temporal;
GRANT ALL PRIVILEGES ON DATABASE temporal_visibility TO temporal;
-- Connect to each database and grant schema permissions:
\c temporal
GRANT ALL ON SCHEMA public TO temporal;
\c temporal_visibility
GRANT ALL ON SCHEMA public TO temporal;
```

Then set the Terraform variables:

```hcl
temporal_enabled       = true
aurora_host            = "your-aurora-cluster.cluster-xxxxx.us-east-1.rds.amazonaws.com"
aurora_port            = 5432
temporal_db_user       = "temporal"
temporal_chart_version = "1.2.1"  # pin to specific version for reproducible deploys
```

> **Note**: `aurora_host` is required when `temporal_enabled = true`. Terraform will fail validation if it is empty. The Aurora cluster must be provisioned externally (see POST-DEPLOY.md step 2).

## How to Access

### Web UI

```bash
kubectl port-forward svc/temporal-web -n temporal 8080:8080
# Open http://localhost:8080
```

### Admin Tools (tctl)

```bash
kubectl exec -it deploy/temporal-admintools -n temporal -- bash

# List namespaces
tctl namespace list

# Describe the default namespace
tctl namespace describe default

# Register a custom namespace
tctl namespace register my-agents \
  --retention 72h \
  --description "Multi-agent workflow namespace"

# List workflows
tctl workflow list

# Describe a workflow
tctl workflow describe --workflow-id <id>
```

## How to Connect Workers

The Temporal Server is SDK-agnostic — all 7 official SDKs connect via gRPC to the same endpoint. No server-side configuration needed per SDK.

**Endpoint**: `temporal-frontend.temporal.svc.cluster.local:7233`

### Go

```go
import "go.temporal.io/sdk/client"

c, err := client.Dial(client.Options{
    HostPort:  "temporal-frontend.temporal.svc.cluster.local:7233",
    Namespace: "default",
})
if err != nil {
    log.Fatal(err)
}
defer c.Close()
```

### Python

```python
from temporalio.client import Client

client = await Client.connect(
    "temporal-frontend.temporal.svc.cluster.local:7233",
    namespace="default",
)
```

### TypeScript

```typescript
import { Connection, Client } from '@temporalio/client';

const connection = await Connection.connect({
  address: 'temporal-frontend.temporal.svc.cluster.local:7233',
});
const client = new Client({
  connection,
  namespace: 'default',
});
```

### With mTLS (Go example)

Workers must present a certificate signed by the Temporal CA. Extract the CA cert and generate worker certs:

```bash
# Extract CA cert from the cluster
kubectl get secret temporal-tls -n temporal -o jsonpath='{.data.ca\.crt}' | base64 -d > ca.crt
```

```go
import (
    "crypto/tls"
    "crypto/x509"
    "os"
    "go.temporal.io/sdk/client"
)

cert, err := tls.LoadX509KeyPair("worker.crt", "worker.key")
if err != nil {
    log.Fatal(err)
}

caCert, err := os.ReadFile("ca.crt")
if err != nil {
    log.Fatal(err)
}
caCertPool := x509.NewCertPool()
caCertPool.AppendCertsFromPEM(caCert)

c, err := client.Dial(client.Options{
    HostPort:  "temporal-frontend.temporal.svc.cluster.local:7233",
    Namespace: "default",
    ConnectionOptions: client.ConnectionOptions{
        TLS: &tls.Config{
            Certificates: []tls.Certificate{cert},
            RootCAs:      caCertPool,
        },
    },
})
```

## Cross-Language Task Queue Routing

Multiple SDKs can connect simultaneously. Route activities to specific language workers via task queues:

```go
// Go workflow calling a Python activity
func AgentWorkflow(ctx workflow.Context, input AgentInput) (AgentResult, error) {
    // Execute Go activity on go-tasks queue
    var analysisResult AnalysisResult
    err := workflow.ExecuteActivity(
        workflow.WithTaskQueue(ctx, "go-tasks"),
        AnalyzeActivity,
        input,
    ).Get(ctx, &analysisResult)

    // Execute Python ML activity on python-tasks queue
    var mlResult MLResult
    ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
        TaskQueue:           "python-tasks",
        StartToCloseTimeout: 5 * time.Minute,
    })
    err = workflow.ExecuteActivity(ctx, "run_ml_model", analysisResult).Get(ctx, &mlResult)

    return AgentResult{Analysis: analysisResult, ML: mlResult}, nil
}
```

Each language runs its own worker polling a distinct task queue:

```go
// Go worker
w := worker.New(c, "go-tasks", worker.Options{})
w.RegisterActivity(AnalyzeActivity)
```

```python
# Python worker
worker = Worker(client, task_queue="python-tasks", activities=[run_ml_model])
await worker.run()
```

## Workflow Patterns for E2B

### Sequential Multi-Agent

Activities run in sequence — each step's output feeds the next:

```go
func SequentialAgentWorkflow(ctx workflow.Context, task string) (string, error) {
    aopts := workflow.ActivityOptions{
        StartToCloseTimeout: 10 * time.Minute,
    }
    ctx = workflow.WithActivityOptions(ctx, aopts)

    var planResult string
    err := workflow.ExecuteActivity(ctx, PlannerAgent, task).Get(ctx, &planResult)
    if err != nil {
        return "", err
    }

    var codeResult string
    err = workflow.ExecuteActivity(ctx, CoderAgent, planResult).Get(ctx, &codeResult)
    if err != nil {
        return "", err
    }

    var reviewResult string
    err = workflow.ExecuteActivity(ctx, ReviewerAgent, codeResult).Get(ctx, &reviewResult)
    if err != nil {
        return "", err
    }

    return reviewResult, nil
}
```

### Human-in-the-Loop (Signal + Wait)

Workflow pauses for human input — consumes zero CPU while waiting:

```go
func HumanInLoopWorkflow(ctx workflow.Context, task string) (string, error) {
    // Agent produces a result
    var draft string
    err := workflow.ExecuteActivity(ctx, DraftAgent, task).Get(ctx, &draft)
    if err != nil {
        return "", err
    }

    // Wait for human approval (zero CPU during wait)
    approvalCh := workflow.GetSignalChannel(ctx, "approval")
    var approval ApprovalSignal
    approvalCh.Receive(ctx, &approval)

    if !approval.Approved {
        return "", fmt.Errorf("rejected: %s", approval.Reason)
    }

    // Continue after approval
    var result string
    err = workflow.ExecuteActivity(ctx, FinalizeAgent, draft).Get(ctx, &result)
    return result, err
}

// Signal from API handler:
// client.SignalWorkflow(ctx, workflowID, "", "approval", ApprovalSignal{Approved: true})
```

### Fire-and-Forget (Child Workflow)

Launch a long-running workflow that outlives the parent:

```go
func OrchestratorWorkflow(ctx workflow.Context, tasks []string) error {
    for _, task := range tasks {
        childOpts := workflow.ChildWorkflowOptions{
            ParentClosePolicy: enums.PARENT_CLOSE_POLICY_ABANDON,
        }
        ctx := workflow.WithChildOptions(ctx, childOpts)
        workflow.ExecuteChildWorkflow(ctx, BackgroundAgentWorkflow, task)
        // Don't wait for result — fire and forget
    }
    return nil
}
```

### Long-Running Async (Manual Activity Completion)

For activities that take hours/days — the activity returns immediately, completes later via task token:

```go
func LongRunningActivity(ctx context.Context, input Input) (Result, error) {
    // Get the task token for later completion
    taskToken := activity.GetInfo(ctx).TaskToken

    // Store task token (e.g., in Redis) for external completion
    storeTaskToken(input.ID, taskToken)

    // Return ErrResultPending — activity stays open but releases the worker
    return Result{}, activity.ErrResultPending
}

// Later, when the external process completes:
// client.CompleteActivity(ctx, taskToken, result, nil)
```

## How Activities Communicate with VMs

Activities interact with E2B sandboxes through existing gRPC services:

### Sandbox Lifecycle (via Orchestrator gRPC)

```go
import orchestratorpb "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"

func CreateSandboxActivity(ctx context.Context, templateID string) (string, error) {
    conn, _ := grpc.Dial("orchestrator:5008", grpc.WithInsecure())
    client := orchestratorpb.NewSandboxServiceClient(conn)

    resp, err := client.Create(ctx, &orchestratorpb.SandboxCreateRequest{
        TemplateId: templateID,
    })
    return resp.SandboxId, err
}
```

### Process Execution (via Envd Connect RPC)

```go
import "github.com/e2b-dev/infra/packages/shared/pkg/grpc/envd_command"

func ExecuteInSandboxActivity(ctx context.Context, sandboxID, cmd string) (string, error) {
    // SetSandboxHeader routes through client-proxy to the correct sandbox
    ctx = envd_command.SetSandboxHeader(ctx, sandboxID)

    // Execute process via Envd
    result, err := envd_command.RunProcess(ctx, cmd)
    return result.Stdout, err
}
```

## Security

### mTLS

All Temporal services communicate over mTLS:

- **Internode**: Frontend ↔ History ↔ Matching ↔ Worker use shared internode certs
- **Frontend**: Workers/clients must present certs signed by the Temporal CA

Certificates are stored in K8s secret `temporal-tls` in the `temporal` namespace. The CA cert, internode cert/key, and frontend cert/key are all managed by Terraform.

### Network Policy

Kubernetes NetworkPolicy restricts access:

- **Within `temporal` namespace**: All traffic allowed (internode)
- **From `e2b` namespace**: Only Frontend (7233) and Web UI (8080) ports
- **All other namespaces**: Blocked

To allow workers from a different namespace, add an ingress rule:

```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: temporal-custom-access
  namespace: temporal
spec:
  podSelector: {}
  ingress:
    - from:
        - namespaceSelector:
            matchLabels:
              kubernetes.io/metadata.name: your-namespace
      ports:
        - port: 7233
          protocol: TCP
  policyTypes:
    - Ingress
```

### OIDC / SSO for Web UI

The Web UI initially uses `noopAuthorizer` (permits all). Since access is via `kubectl port-forward` only, this is acceptable. To enable OIDC SSO:

```yaml
# Add to Helm values (web section)
web:
  additionalEnv:
    - name: TEMPORAL_AUTH_ENABLED
      value: "true"
    - name: TEMPORAL_AUTH_PROVIDER_URL
      value: "https://your-idp.example.com"
    - name: TEMPORAL_AUTH_CLIENT_ID
      value: "temporal-web"
    - name: TEMPORAL_AUTH_CLIENT_SECRET
      valueFrom:
        secretKeyRef:
          name: temporal-oidc
          key: client-secret
    - name: TEMPORAL_AUTH_CALLBACK_URL
      value: "https://temporal.yourdomain.com/auth/sso/callback"
```

### RBAC (Multi-Tenant Production)

For multi-tenant access control, implement a ClaimMapper + Authorizer plugin:

```go
// ClaimMapper maps OIDC claims to Temporal authorization info
type CustomClaimMapper struct{}

func (c *CustomClaimMapper) GetClaims(authInfo *authorization.AuthInfo) (*authorization.Claims, error) {
    // Extract namespace permissions from OIDC token claims
    return &authorization.Claims{
        Namespaces: map[string]authorization.Role{
            "team-a": authorization.RoleWriter,
            "team-b": authorization.RoleReader,
        },
    }, nil
}
```

> **Warning**: The default `noopAuthorizer` permits all operations. Configure ClaimMapper + Authorizer before exposing Temporal to multiple teams.

## Monitoring

### Prometheus Metrics

Temporal exports metrics on each service's metrics port. The Helm chart enables Prometheus annotations automatically. Key metrics:

| Metric | Description |
|--------|-------------|
| `temporal_workflow_completed` | Completed workflows (by namespace, type) |
| `temporal_workflow_failed` | Failed workflows |
| `temporal_workflow_task_schedule_to_start_latency` | Task queue latency |
| `temporal_activity_schedule_to_start_latency` | Activity dispatch latency |
| `temporal_persistence_latency` | Database operation latency |

### OTel Integration

If your OTel collector scrapes Prometheus targets, Temporal metrics are auto-discovered via pod annotations. No additional configuration needed beyond what's already in the E2B OTel stack.

### Grafana Dashboards

Temporal provides community Grafana dashboards:
- Server: ID `10271` (Server metrics)
- SDK: ID `10272` (Worker/SDK metrics)

Import via Grafana UI → Dashboards → Import → Dashboard ID.

## Production Checklist

- [ ] **Shard count**: `numHistoryShards = 512` is set and CANNOT be changed after first deploy
- [ ] **Aurora databases**: `temporal` and `temporal_visibility` created with correct grants
- [ ] **mTLS enforced**: Internode and frontend TLS enabled (default in this deployment)
- [ ] **Worker certs**: Generate and distribute client certificates signed by Temporal CA
- [ ] **Network Policy**: Verify only `e2b` namespace can reach Frontend port
- [ ] **Namespace registration**: Register application namespaces via `tctl namespace register`
- [ ] **Retention policy**: Set workflow history retention per namespace (default: 72h)
- [ ] **OIDC (if multi-tenant)**: Configure SSO before exposing Web UI externally
- [ ] **Backups**: Aurora automated backups cover Temporal databases
- [ ] **Upgrades**: Follow [Temporal upgrade guide](https://docs.temporal.io/self-hosted-guide/upgrade-server) — always upgrade schema before server
- [ ] **Monitoring**: Verify Prometheus metrics appear in Grafana

## Verification

After `terraform apply`:

```bash
# All pods running
kubectl get pods -n temporal

# Schema jobs completed
kubectl get jobs -n temporal

# Web UI accessible
kubectl port-forward svc/temporal-web -n temporal 8080:8080

# Frontend reachable from e2b namespace
kubectl run test --rm -it --image=busybox -n e2b -- \
  nc -z temporal-frontend.temporal.svc.cluster.local 7233

# Admin tools working
kubectl exec -it deploy/temporal-admintools -n temporal -- \
  tctl namespace list
```
