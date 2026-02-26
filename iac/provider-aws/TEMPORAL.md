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

## End-to-End Guide: Multi-Agent Backend with WebSocket API

You bring the frontend. We provide the agentic backend runtime.

This guide walks through building a Python multi-agent backend that runs inside a Firecracker VM, exposes a WebSocket API, and connects to your React app. The VM runs everything — FastAPI server, Temporal worker, LLM calls, code execution — in complete isolation. Your frontend connects via WebSocket, sends tasks, and receives streaming results.

### Step 1: Architecture

```
    Your React App                          EKS Cluster
    ┌──────────────┐              ┌──────────────────────────────┐
    │              │   WebSocket  │                              │
    │  useAgent()  ├──────────────┼──► Client-Proxy              │
    │              │              │        │                     │
    └──────┬───────┘              │        ▼                     │
           │                      │   Orchestrator Proxy (:5007) │
           │                      │        │                     │
    ┌──────▼───────┐              │        │    Temporal Frontend │
    │ Your Backend │   E2B API    │        │    (:7233, NLB)     │
    │              ├──────────────┼──►  E2B API                  │
    │ POST /start  │              │        │                     │
    │ DELETE /stop │              └────────┼─────────────────────┘
    └──────────────┘                       │
                                           ▼
                              ┌─────────────────────────────┐
                              │  Firecracker VM             │
                              │                             │
                              │  envd → python3 server.py   │
                              │  ┌───────────────────────┐  │
                              │  │ FastAPI (:8000)       │  │
                              │  │  └─ /ws endpoint      │◄─┼── WebSocket
                              │  │                       │  │
                              │  │ Temporal Worker       │  │
                              │  │  └─ polls Frontend ───┼──┼──► Temporal
                              │  │                       │  │
                              │  │ Activities:           │  │
                              │  │  call_llm() ──────────┼──┼──► Anthropic API
                              │  │  execute_code()       │  │    (outbound NAT)
                              │  │  (subprocess.run)     │  │
                              │  └───────────────────────┘  │
                              └─────────────────────────────┘
```

**What runs where:**

| Component | Where | How |
|-----------|-------|-----|
| React app | User's browser | Connects via WebSocket to VM |
| Provisioning backend | User's server | Creates/destroys VMs via E2B API |
| FastAPI + Temporal Worker | Inside Firecracker VM | Auto-started by envd at VM boot |
| LLM calls | Outbound from VM via NAT | `anthropic` SDK → internet via host MASQUERADE |
| Code execution | Inside Firecracker VM | `subprocess.run()` with tempfile, local to VM |
| Temporal Server | EKS system nodes | Manages workflow state, dispatches tasks |
| Workflow state | Aurora PostgreSQL | Persisted by Temporal Server |

**The lifecycle:**

1. **Provision** — Your backend calls E2B API → VM boots → envd auto-starts `server.py` → FastAPI listens on `:8000`, Temporal worker polls Frontend
2. **Connect** — Your backend returns `wss://8000-{sandbox_id}.{domain}/ws` to the React app → React opens WebSocket → Client-Proxy routes to VM → envd's port scanner auto-forwards port 8000 via `socat`
3. **Interact** — React sends task via WebSocket → FastAPI starts Temporal workflow → streams status/artifacts back as the workflow progresses
4. **Tear down** — Your backend calls E2B API to kill the VM, or VM TTL expires

### Step 2: Write the Agent Backend

This is a normal Python project. Nothing E2B-specific in the application code.

#### Project Structure

```
my-agent-backend/
├── server.py                   # FastAPI + WebSocket + Temporal worker (entry point)
├── workflows/
│   ├── __init__.py
│   └── agent_workflow.py       # Workflow definition (deterministic)
├── activities/
│   ├── __init__.py
│   ├── llm_activities.py       # LLM API calls (outbound via NAT)
│   └── execution_activities.py # Local code execution (subprocess)
├── e2b.Dockerfile              # VM template definition
└── requirements.txt
```

#### Dependencies

```
# requirements.txt
temporalio>=1.9.0
anthropic>=0.40.0
pydantic>=2.0.0
fastapi>=0.115.0
uvicorn>=0.34.0
```

No `e2b-code-interpreter` — the code runs inside the VM via `subprocess.run()`.

#### Data Models

```python
# workflows/__init__.py
from dataclasses import dataclass, field


@dataclass
class AgentTask:
    """Input to the multi-agent workflow."""
    task_description: str
    language: str = "python"
    max_iterations: int = 3
    require_approval: bool = False


@dataclass
class AgentResult:
    """Output from the multi-agent workflow."""
    plan: str
    code: str
    output: str
    review: str
    approved: bool


@dataclass
class ApprovalSignal:
    """Signal sent by a human to approve/reject."""
    approved: bool
    feedback: str = ""
```

#### Activities: Code Execution (Local)

Code runs locally inside the VM via `subprocess.run()`. No sandbox SDK, no network calls — the VM itself is the sandbox.

```python
# activities/execution_activities.py
import os
import subprocess
import tempfile
from temporalio import activity


@activity.defn
async def execute_code(code: str, language: str) -> str:
    """Execute code locally inside the Firecracker VM."""
    activity.logger.info(f"Executing {language} code ({len(code)} chars)")

    suffix = {"python": ".py", "javascript": ".js", "bash": ".sh"}.get(language, ".py")
    with tempfile.NamedTemporaryFile(mode="w", suffix=suffix, delete=False) as f:
        f.write(code)
        code_path = f.name

    try:
        cmd = {
            "python": ["python3", code_path],
            "javascript": ["node", code_path],
            "bash": ["bash", code_path],
        }.get(language, ["python3", code_path])

        result = subprocess.run(
            cmd,
            capture_output=True,
            text=True,
            timeout=120,
            cwd="/tmp",
        )

        output_parts = []
        if result.stdout:
            output_parts.append(result.stdout)
        if result.stderr:
            output_parts.append(f"STDERR: {result.stderr}")
        if result.returncode != 0:
            output_parts.append(f"Exit code: {result.returncode}")

        return "\n".join(output_parts) if output_parts else "(no output)"

    except subprocess.TimeoutExpired:
        return "ERROR: Code execution timed out after 120 seconds"
    finally:
        os.unlink(code_path)
```

#### Activities: LLM Calls (Outbound via NAT)

LLM calls go outbound from the VM through the host's NAT (SNAT → MASQUERADE) to reach the Anthropic API. No special configuration needed — the VM has internet access via the orchestrator's network namespace routing.

```python
# activities/llm_activities.py
import os
from temporalio import activity
from anthropic import Anthropic

LLM_API_KEY = os.environ.get("ANTHROPIC_API_KEY", "")
LLM_MODEL = os.environ.get("LLM_MODEL", "claude-sonnet-4-20250514")


@activity.defn
async def call_planner(task_description: str) -> str:
    """Ask the LLM to create a plan for the task."""
    client = Anthropic(api_key=LLM_API_KEY)

    response = client.messages.create(
        model=LLM_MODEL,
        max_tokens=2048,
        messages=[{
            "role": "user",
            "content": f"""You are a planning agent. Create a step-by-step plan
to accomplish this task. Output ONLY the plan, no code yet.

Task: {task_description}"""
        }],
    )
    return response.content[0].text


@activity.defn
async def call_coder(plan: str, language: str) -> str:
    """Ask the LLM to write code based on the plan."""
    client = Anthropic(api_key=LLM_API_KEY)

    response = client.messages.create(
        model=LLM_MODEL,
        max_tokens=4096,
        messages=[{
            "role": "user",
            "content": f"""You are a coding agent. Write {language} code that
implements this plan. Output ONLY the code, no explanation.
The code must be self-contained and print its results to stdout.

Plan:
{plan}"""
        }],
    )

    code = response.content[0].text
    if code.startswith("```"):
        lines = code.split("\n")
        code = "\n".join(lines[1:-1]) if lines[-1].strip() == "```" else "\n".join(lines[1:])
    return code


@activity.defn
async def call_reviewer(code: str, output: str, task_description: str) -> str:
    """Ask the LLM to review the code and its output."""
    client = Anthropic(api_key=LLM_API_KEY)

    response = client.messages.create(
        model=LLM_MODEL,
        max_tokens=2048,
        messages=[{
            "role": "user",
            "content": f"""You are a code review agent. Review whether this code
correctly accomplishes the original task. Be concise.

Original task: {task_description}

Code:
```
{code}
```

Execution output:
```
{output}
```

Does this correctly solve the task? Respond with:
VERDICT: PASS or FAIL
Then a brief explanation."""
        }],
    )
    return response.content[0].text
```

#### Workflow

The workflow is **deterministic** — no direct I/O, no randomness, no current time. All side effects happen in activities. It emits events to a list that the WebSocket server polls and streams to the client.

```python
# workflows/agent_workflow.py
from datetime import timedelta
from temporalio import workflow
from temporalio.common import RetryPolicy

with workflow.unsafe.imports_passed_through():
    from activities.execution_activities import execute_code
    from activities.llm_activities import call_planner, call_coder, call_reviewer
    from workflows import AgentTask, AgentResult, ApprovalSignal


@workflow.defn
class MultiAgentWorkflow:
    """
    Multi-agent pipeline with event streaming:
      Plan → Code → Execute → Review → Retry loop → Optional approval
    """

    def __init__(self):
        self._events: list[dict] = []
        self.approval: ApprovalSignal | None = None

    @workflow.signal
    async def approve(self, signal: ApprovalSignal):
        self.approval = signal

    @workflow.query
    def get_events(self) -> list[dict]:
        """Returns all events emitted so far. The WebSocket server polls this."""
        return self._events

    @workflow.run
    async def run(self, task: AgentTask) -> AgentResult:
        llm_timeout = timedelta(minutes=5)
        llm_retry = RetryPolicy(
            initial_interval=timedelta(seconds=1),
            maximum_interval=timedelta(seconds=30),
            maximum_attempts=3,
        )
        exec_timeout = timedelta(minutes=3)
        exec_retry = RetryPolicy(maximum_attempts=2)

        # --- Plan ---
        self._events.append({"type": "status", "step": "planning"})
        plan = await workflow.execute_activity(
            call_planner, task.task_description,
            start_to_close_timeout=llm_timeout, retry_policy=llm_retry,
        )
        self._events.append({"type": "artifact", "name": "plan", "data": plan})

        code = ""
        output = ""
        review = ""

        for iteration in range(task.max_iterations):
            # --- Code ---
            self._events.append({
                "type": "status", "step": "coding", "iteration": iteration + 1,
            })
            code = await workflow.execute_activity(
                call_coder, plan, task.language,
                start_to_close_timeout=llm_timeout, retry_policy=llm_retry,
            )
            self._events.append({"type": "artifact", "name": "code", "data": code})

            # --- Execute ---
            self._events.append({
                "type": "status", "step": "executing", "iteration": iteration + 1,
            })
            output = await workflow.execute_activity(
                execute_code, code, task.language,
                start_to_close_timeout=exec_timeout,
                heartbeat_timeout=timedelta(minutes=1),
                retry_policy=exec_retry,
            )
            self._events.append({"type": "artifact", "name": "output", "data": output})

            # --- Review ---
            self._events.append({
                "type": "status", "step": "reviewing", "iteration": iteration + 1,
            })
            review = await workflow.execute_activity(
                call_reviewer, code, output, task.task_description,
                start_to_close_timeout=llm_timeout, retry_policy=llm_retry,
            )
            self._events.append({"type": "artifact", "name": "review", "data": review})

            if "VERDICT: PASS" in review:
                break

            plan = f"{plan}\n\nPrevious attempt failed. Reviewer feedback:\n{review}"

        # --- Approval ---
        if task.require_approval:
            self._events.append({"type": "status", "step": "waiting_for_approval"})

            await workflow.wait_condition(
                lambda: self.approval is not None,
                timeout=timedelta(hours=24),
            )

            if self.approval is None:
                raise workflow.ApplicationError("Approval timed out after 24h")
            if not self.approval.approved:
                raise workflow.ApplicationError(
                    f"Rejected by human: {self.approval.feedback}"
                )

        self._events.append({"type": "status", "step": "completed"})
        return AgentResult(
            plan=plan, code=code, output=output, review=review,
            approved=self.approval.approved if self.approval else True,
        )
```

#### Server (FastAPI + WebSocket + Temporal Worker)

This is the entry point that runs inside the VM. It starts both a FastAPI server (for WebSocket connections from your React app) and a Temporal worker (for executing workflows) in the same process.

```python
# server.py
import asyncio
import os
import uuid
from contextlib import asynccontextmanager

from fastapi import FastAPI, WebSocket, WebSocketDisconnect
from temporalio.client import Client
from temporalio.worker import Worker

from workflows.agent_workflow import MultiAgentWorkflow
from workflows import AgentTask, ApprovalSignal
from activities.execution_activities import execute_code
from activities.llm_activities import call_planner, call_coder, call_reviewer


TEMPORAL_HOST = os.environ.get("TEMPORAL_HOST")
TEMPORAL_NAMESPACE = os.environ.get("TEMPORAL_NAMESPACE", "default")
TASK_QUEUE = os.environ.get("TASK_QUEUE", "agent-tasks")

temporal_client: Client | None = None


@asynccontextmanager
async def lifespan(app: FastAPI):
    global temporal_client

    if not TEMPORAL_HOST:
        raise RuntimeError(
            "TEMPORAL_HOST must be set (e.g. temporal-nlb.internal:7233). "
            "K8s ClusterIP DNS is not resolvable from inside a VM."
        )

    temporal_client = await Client.connect(
        TEMPORAL_HOST, namespace=TEMPORAL_NAMESPACE
    )

    worker = Worker(
        temporal_client,
        task_queue=TASK_QUEUE,
        workflows=[MultiAgentWorkflow],
        activities=[execute_code, call_planner, call_coder, call_reviewer],
    )

    worker_task = asyncio.create_task(worker.run())
    print(f"Agent server ready — Temporal: {TEMPORAL_HOST}, queue: {TASK_QUEUE}")

    yield

    worker_task.cancel()


app = FastAPI(lifespan=lifespan)


@app.get("/health")
async def health():
    return {"status": "ok"}


@app.websocket("/ws")
async def agent_session(ws: WebSocket):
    await ws.accept()

    try:
        # 1. Receive task from React client
        msg = await ws.receive_json()
        task = AgentTask(
            task_description=msg["task"],
            language=msg.get("language", "python"),
            max_iterations=msg.get("max_iterations", 3),
            require_approval=msg.get("require_approval", False),
        )

        # 2. Start Temporal workflow
        workflow_id = f"agent-{uuid.uuid4().hex[:12]}"
        handle = await temporal_client.start_workflow(
            MultiAgentWorkflow.run,
            task,
            id=workflow_id,
            task_queue=TASK_QUEUE,
        )
        await ws.send_json({"type": "connected", "workflow_id": workflow_id})

        # 3. Stream events from workflow to client
        result_task = asyncio.create_task(handle.result())
        last_event_idx = 0

        while not result_task.done():
            # Poll workflow for new events
            events = await handle.query(MultiAgentWorkflow.get_events)
            for event in events[last_event_idx:]:
                await ws.send_json(event)
            last_event_idx = len(events)

            # Check for client messages (approval signals) — non-blocking
            try:
                client_msg = await asyncio.wait_for(
                    ws.receive_json(), timeout=1.0
                )
                if client_msg.get("type") == "approve":
                    await handle.signal(
                        MultiAgentWorkflow.approve,
                        ApprovalSignal(
                            approved=client_msg.get("approved", True),
                            feedback=client_msg.get("feedback", ""),
                        ),
                    )
            except asyncio.TimeoutError:
                pass

        # 4. Send final result
        events = await handle.query(MultiAgentWorkflow.get_events)
        for event in events[last_event_idx:]:
            await ws.send_json(event)

        try:
            result = result_task.result()
            await ws.send_json({
                "type": "result",
                "plan": result.plan,
                "code": result.code,
                "output": result.output,
                "review": result.review,
                "approved": result.approved,
            })
        except Exception as e:
            await ws.send_json({"type": "error", "message": str(e)})

    except WebSocketDisconnect:
        # Client disconnected — the workflow keeps running in Temporal.
        # The client can reconnect and query the workflow by ID.
        pass


if __name__ == "__main__":
    import uvicorn
    uvicorn.run(app, host="0.0.0.0", port=8000)
```

**Key design decisions:**
- FastAPI and Temporal worker share the same asyncio event loop — the worker picks up tasks dispatched to this VM
- The WebSocket handler polls the workflow's `get_events` query every second and streams new events to the client
- If the client disconnects, the Temporal workflow keeps running — it's durable
- Approval signals arrive as WebSocket messages and are forwarded as Temporal signals

### Step 3: Connect from React

#### WebSocket Hook

```typescript
// hooks/useAgent.ts
import { useState, useRef, useCallback } from "react";

interface AgentEvent {
  type: "connected" | "status" | "artifact" | "result" | "error";
  step?: string;
  iteration?: number;
  name?: string;
  data?: string;
  workflow_id?: string;
  plan?: string;
  code?: string;
  output?: string;
  review?: string;
  approved?: boolean;
  message?: string;
}

export function useAgent() {
  const [events, setEvents] = useState<AgentEvent[]>([]);
  const [status, setStatus] = useState<string>("idle");
  const wsRef = useRef<WebSocket | null>(null);
  const sandboxIdRef = useRef<string | null>(null);

  const start = useCallback(async (task: string, options?: {
    language?: string;
    maxIterations?: number;
    requireApproval?: boolean;
  }) => {
    setEvents([]);
    setStatus("provisioning");

    // 1. Ask your backend to create a VM
    const res = await fetch("/api/agent/start", { method: "POST" });
    const { sandbox_id, ws_url } = await res.json();
    sandboxIdRef.current = sandbox_id;

    // 2. Connect WebSocket to the VM
    const ws = new WebSocket(ws_url);
    wsRef.current = ws;

    ws.onopen = () => {
      setStatus("connected");
      ws.send(JSON.stringify({
        task,
        language: options?.language ?? "python",
        max_iterations: options?.maxIterations ?? 3,
        require_approval: options?.requireApproval ?? false,
      }));
    };

    ws.onmessage = (e) => {
      const event: AgentEvent = JSON.parse(e.data);
      setEvents((prev) => [...prev, event]);

      if (event.type === "status") setStatus(event.step ?? "running");
      if (event.type === "result") setStatus("completed");
      if (event.type === "error") setStatus("error");
    };

    ws.onclose = () => {
      if (status !== "completed" && status !== "error") {
        setStatus("disconnected");
      }
    };
  }, []);

  const approve = useCallback((approved: boolean, feedback = "") => {
    wsRef.current?.send(JSON.stringify({
      type: "approve", approved, feedback,
    }));
  }, []);

  const stop = useCallback(async () => {
    wsRef.current?.close();
    if (sandboxIdRef.current) {
      await fetch(`/api/agent/${sandboxIdRef.current}`, { method: "DELETE" });
      sandboxIdRef.current = null;
    }
    setStatus("idle");
  }, []);

  return { events, status, start, approve, stop };
}
```

#### Example Component

```tsx
// components/AgentPanel.tsx
import { useAgent } from "../hooks/useAgent";

export function AgentPanel() {
  const { events, status, start, approve, stop } = useAgent();

  const latestArtifact = (name: string) =>
    [...events].reverse().find((e) => e.type === "artifact" && e.name === name);

  return (
    <div>
      <h2>Agent — {status}</h2>

      <form onSubmit={(e) => {
        e.preventDefault();
        const task = new FormData(e.currentTarget).get("task") as string;
        start(task, { requireApproval: true });
      }}>
        <input name="task" placeholder="Describe your task..." />
        <button type="submit" disabled={status !== "idle"}>Run</button>
      </form>

      {latestArtifact("plan") && (
        <section>
          <h3>Plan</h3>
          <pre>{latestArtifact("plan")?.data}</pre>
        </section>
      )}

      {latestArtifact("code") && (
        <section>
          <h3>Code</h3>
          <pre>{latestArtifact("code")?.data}</pre>
        </section>
      )}

      {latestArtifact("output") && (
        <section>
          <h3>Output</h3>
          <pre>{latestArtifact("output")?.data}</pre>
        </section>
      )}

      {latestArtifact("review") && (
        <section>
          <h3>Review</h3>
          <pre>{latestArtifact("review")?.data}</pre>
        </section>
      )}

      {status === "waiting_for_approval" && (
        <div>
          <button onClick={() => approve(true)}>Approve</button>
          <button onClick={() => approve(false, "Needs changes")}>Reject</button>
        </div>
      )}

      {(status === "completed" || status === "error") && (
        <button onClick={stop}>Stop VM</button>
      )}
    </div>
  );
}
```

#### WebSocket Message Protocol

**Client → Server (VM):**

| Message | When |
|---------|------|
| `{"task": "...", "language": "python", "max_iterations": 3, "require_approval": false}` | After WebSocket opens |
| `{"type": "approve", "approved": true, "feedback": ""}` | When user clicks Approve/Reject |

**Server (VM) → Client:**

| Message | When |
|---------|------|
| `{"type": "connected", "workflow_id": "agent-a1b2c3"}` | Workflow started |
| `{"type": "status", "step": "planning"}` | Each phase begins |
| `{"type": "artifact", "name": "plan", "data": "..."}` | Each artifact produced |
| `{"type": "status", "step": "coding", "iteration": 1}` | Iteration begins |
| `{"type": "artifact", "name": "code", "data": "..."}` | Code generated |
| `{"type": "status", "step": "executing", "iteration": 1}` | Execution begins |
| `{"type": "artifact", "name": "output", "data": "..."}` | Execution output |
| `{"type": "status", "step": "reviewing", "iteration": 1}` | Review begins |
| `{"type": "artifact", "name": "review", "data": "..."}` | Review result |
| `{"type": "status", "step": "waiting_for_approval"}` | Awaiting human |
| `{"type": "result", "plan": "...", "code": "...", ...}` | Workflow completed |
| `{"type": "error", "message": "..."}` | Workflow failed |

### Step 4: Package as a VM Template

#### Dockerfile

```dockerfile
# e2b.Dockerfile
FROM e2bdev/base:latest

COPY requirements.txt /home/user/app/requirements.txt
RUN pip install --no-cache-dir -r /home/user/app/requirements.txt

COPY workflows/ /home/user/app/workflows/
COPY activities/ /home/user/app/activities/
COPY server.py /home/user/app/server.py
```

#### Build the Template

```bash
e2b template build \
  --name "agent-worker" \
  --dockerfile e2b.Dockerfile \
  --start-cmd "cd /home/user/app && python3 server.py" \
  --cpu-count 2 \
  --memory-mb 1024
```

**What happens at boot:** The VM starts → envd's `InitializeStartProcess()` runs the `StartCmd` → `server.py` starts → uvicorn listens on `0.0.0.0:8000` → envd's port scanner detects port 8000 and auto-starts a `socat` forwarder (`169.254.0.21:8000 → localhost:8000`) → the port is now reachable from outside the VM. No port configuration needed — it's automatic.

### Step 5: Deploy and Run

#### One-Time: Expose Temporal Frontend

**K8s ClusterIP DNS is NOT resolvable from inside VMs.** VMs run in isolated network namespaces — they are not part of the K8s pod network. You need an NLB endpoint:

```bash
# Get existing LB, or create one
TEMPORAL_HOST=$(kubectl get svc temporal-frontend -n temporal \
  -o jsonpath='{.status.loadBalancer.ingress[0].hostname}'):7233

# If no LB exists:
kubectl expose svc temporal-frontend -n temporal \
  --name=temporal-frontend-nlb \
  --type=LoadBalancer \
  --port=7233 --target-port=7233
kubectl annotate svc temporal-frontend-nlb -n temporal \
  service.beta.kubernetes.io/aws-load-balancer-internal="true" \
  service.beta.kubernetes.io/aws-load-balancer-type="nlb"

TEMPORAL_HOST=$(kubectl get svc temporal-frontend-nlb -n temporal \
  -o jsonpath='{.status.loadBalancer.ingress[0].hostname}'):7233
```

This NLB hostname resolves via public DNS and is routable from VMs through their NAT path: VM (169.254.0.21) → SNAT to host IP → MASQUERADE to node IP → NLB → Temporal pod.

#### Provisioning Backend

Add two endpoints to your existing backend. These create and destroy VMs — they run on **your** server, not inside the VM.

**Python (FastAPI):**

```python
# In your backend — not inside the VM
import os
from e2b import Sandbox
from fastapi import FastAPI

app = FastAPI()

E2B_DOMAIN = os.environ["E2B_DOMAIN"]  # e.g. "e2b.app" or your self-hosted domain


@app.post("/api/agent/start")
async def start_agent():
    vm = Sandbox(
        template="agent-worker",
        timeout=3600,  # 1 hour — set longer than expected workflow duration
        envs={
            "TEMPORAL_HOST": os.environ["TEMPORAL_HOST"],
            "ANTHROPIC_API_KEY": os.environ["ANTHROPIC_API_KEY"],
            "TASK_QUEUE": "agent-tasks",
        },
    )
    return {
        "sandbox_id": vm.sandbox_id,
        "ws_url": f"wss://8000-{vm.sandbox_id}.{E2B_DOMAIN}/ws",
    }


@app.delete("/api/agent/{sandbox_id}")
async def stop_agent(sandbox_id: str):
    Sandbox.connect(sandbox_id).kill()
    return {"status": "destroyed"}
```

**curl equivalent:**

```bash
# Create VM
curl -X POST https://api.yourdomain.com/sandboxes \
  -H "Authorization: Bearer $E2B_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "templateID": "agent-worker",
    "timeout": 3600,
    "envs": {
      "TEMPORAL_HOST": "'$TEMPORAL_HOST'",
      "TASK_QUEUE": "agent-tasks",
      "ANTHROPIC_API_KEY": "'$ANTHROPIC_API_KEY'"
    }
  }'
# Response: {"sandboxID": "isv6ril5xadwn1k9t2jye", ...}
# WebSocket URL: wss://8000-isv6ril5xadwn1k9t2jye.{domain}/ws

# Destroy VM
curl -X DELETE https://api.yourdomain.com/sandboxes/isv6ril5xadwn1k9t2jye \
  -H "Authorization: Bearer $E2B_API_KEY"
```

Secrets (`ANTHROPIC_API_KEY`, `TEMPORAL_HOST`) are injected as env vars at VM creation time — they are **not** baked into the template image.

#### The Full Sequence

```
User clicks "Run" in React app
  │
  ▼
React → POST /api/agent/start (your backend)
  │
  ▼
Your backend → E2B API: create VM from "agent-worker" template
  │     Injects: TEMPORAL_HOST, ANTHROPIC_API_KEY, TASK_QUEUE
  │
  ▼
VM boots (~1s)
  │  envd starts
  │  envd runs StartCmd: python3 server.py
  │  FastAPI listens on :8000
  │  Temporal worker connects to Frontend via NLB (outbound NAT)
  │  envd port scanner detects :8000 → socat binds 169.254.0.21:8000
  │
  ▼
Your backend → returns {sandbox_id, ws_url} to React
  │
  ▼
React → opens WebSocket to wss://8000-{sandbox_id}.{domain}/ws
  │  DNS → Client-Proxy → Orchestrator Proxy → socat → FastAPI
  │
  ▼
React → sends {"task": "Build a web scraper for ..."}
  │
  ▼
server.py → starts Temporal workflow → streams events back via WebSocket
  │  planning → plan artifact
  │  coding → code artifact
  │  executing (subprocess.run) → output artifact
  │  reviewing → review artifact
  │  (retry loop if VERDICT: FAIL)
  │  waiting_for_approval (if required)
  │
  ▼
React → receives events, renders UI, sends approval if needed
  │
  ▼
Workflow completes → {"type": "result", ...} sent to React
  │
  ▼
User clicks "Stop VM"
  │
  ▼
React → DELETE /api/agent/{sandbox_id} (your backend)
  │
  ▼
Your backend → E2B API: kill VM
  │  Firecracker process terminates, network namespace cleaned up, slot freed
```

#### Debug

```bash
# Temporal Web UI — see workflow history, activity inputs/outputs
kubectl port-forward svc/temporal-web -n temporal 8080:8080
# Open http://localhost:8080

# List running workflows
kubectl exec -it deploy/temporal-admintools -n temporal -- \
  tctl workflow list --open

# Query a specific workflow
kubectl exec -it deploy/temporal-admintools -n temporal -- \
  tctl workflow query --workflow_id "agent-a1b2c3d4e5f6" \
  --query_type get_events
```

### Step 6: Networking Deep Dive

#### Inbound: WebSocket → VM

When your React app opens `wss://8000-{sandbox_id}.{domain}/ws`, the request traverses:

```
Browser
  │
  ▼
Client-Proxy (edge layer, port 443)
  │  Parses hostname: port=8000, sandbox_id=isv6ril5xadwn1k9t2jye
  │  Redis catalog lookup → finds orchestrator node IP
  │
  ▼
Orchestrator Proxy (:5007 on the host node)
  │  Local sandbox map → finds VM's HostIP (10.11.x.x)
  │  Optional: validates traffic access token (E2b-Traffic-Access-Token header)
  │  Forwards to 10.11.x.x:8000
  │
  ▼
VM network namespace
  │  socat: 169.254.0.21:8000 → localhost:8000
  │  (auto-configured by envd port scanner — no manual setup)
  │
  ▼
FastAPI server inside the VM
```

HTTP, WebSocket, and SSE all work through this path. The URL scheme is `{PORT}-{SANDBOX_ID}.{DOMAIN}` — change the port prefix to reach different services inside the same VM.

#### Outbound: VM → Temporal / LLM

When the Temporal worker or Anthropic SDK makes an outbound connection:

```
Process inside VM (e.g. worker connecting to Temporal Frontend)
  │  Source: 169.254.0.21:random
  │
  ▼
TAP device → veth pair
  │  SNAT: 169.254.0.21 → 10.11.x.x (host IP)
  │
  ▼
Host network namespace
  │  MASQUERADE: 10.11.x.x → physical node IP
  │  Routes to destination via node's default gateway
  │
  ▼
Temporal NLB / Anthropic API / any internet endpoint
```

All outbound traffic is allowed by default. Egress filtering can be configured per-sandbox via the orchestrator's firewall rules if needed.

### Step 7: What Happens When Things Fail

Every failure mode is handled by Temporal's durable execution — no manual recovery needed:

| Failure | What Happens |
|---------|-------------|
| VM crashes mid-activity | Activity heartbeat times out → Temporal reschedules on another worker VM |
| VM network partition | Same — heartbeat timeout triggers retry on a healthy worker |
| VM TTL expires | Worker process dies → in-flight activity retried on surviving VMs |
| Orchestrator host node goes down | All VMs on that node lose heartbeat → work redistributed to VMs on other nodes |
| LLM API returns 500 | Activity retry policy kicks in (3 attempts, exponential backoff) |
| `subprocess.run()` timeout | Activity returns error → reviewer sees it → coder retries with feedback |
| WebSocket disconnects | **Workflow keeps running** — Temporal is the source of truth, not the WebSocket. Client can reconnect or query the workflow by ID later. |
| Human doesn't approve within 24h | Workflow raises `ApplicationError` (zero CPU consumed while waiting) |

**VM TTL:** Set the VM `timeout` longer than your expected workflow duration. If a workflow might run for 30 minutes, set timeout to 3600 (1 hour). Use `activity.heartbeat()` in long-running activities so Temporal can detect dead workers promptly.

**Scaling:** To handle more concurrent workflows, create more VMs from the same template — each runs its own Temporal worker. For per-user isolation, create one VM per user session (each user gets their own sandbox with their own WebSocket connection).

Every activity input/output is persisted in Aurora. You can inspect the full execution history in the Temporal Web UI — including what code was generated, what the subprocess output was, and what the LLM reviewer said.

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
