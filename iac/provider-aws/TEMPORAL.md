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

## End-to-End Guide: Python Multi-Agent System

This section walks through building and deploying a Python multi-agent system that uses Temporal for orchestration and Firecracker VMs (E2B sandboxes) for isolated code execution.

### Architecture

```
                                    EKS Cluster
                    ┌──────────────────────────────────────────────┐
                    │                                              │
 HTTP Request ──────┼──► E2B API ──► Temporal Frontend (:7233)     │
                    │       │              │                       │
                    │       │        Temporal History/Matching      │
                    │       │              │                       │
                    │       │    ┌─────────▼──────────┐            │
                    │       │    │  Your Python Worker │            │
                    │       │    │  (K8s Deployment)   │            │
                    │       │    │                     │            │
                    │       │    │  Workflow:           │            │
                    │       │    │   1. call_llm()     │            │
                    │       │    │   2. create_sandbox()│───────┐   │
                    │       │    │   3. run_code()     │       │   │
                    │       │    │   4. call_llm()     │       │   │
                    │       │    │   5. wait_approval()│       │   │
                    │       │    │   6. destroy_sandbox│       │   │
                    │       │    └─────────────────────┘       │   │
                    │       │                                  │   │
                    │       ▼                                  ▼   │
                    │  ┌─────────┐    ┌───────────────────────┐   │
                    │  │ Aurora  │    │   Firecracker VM      │   │
                    │  │ (state) │    │   (sandbox)           │   │
                    │  └─────────┘    │   - runs agent code   │   │
                    │                 │   - isolated env       │   │
                    │                 │   - auto-destroyed     │   │
                    │                 └───────────────────────┘   │
                    └──────────────────────────────────────────────┘
                                          │
                                          ▼
                                    External LLM API
                                    (OpenAI / Anthropic)
```

**What runs where:**

| Component | Where | Purpose |
|-----------|-------|---------|
| Temporal Server | EKS system nodes | Workflow state, task dispatch |
| Your Python Worker | EKS (K8s Deployment) | Polls task queue, executes activities |
| Agent code | Firecracker VM (sandbox) | Isolated code execution |
| LLM calls | External API | AI reasoning |
| Workflow state | Aurora PostgreSQL | Durable execution history |

The worker runs on EKS, not inside the VM. The VM is an ephemeral sandbox that activities create, use, and destroy.

### Prerequisites

- Infrastructure deployed with `temporal_enabled = true`
- Temporal databases created on Aurora (see Pre-Deployment Steps above)
- `tctl namespace register` completed
- E2B API running and accessible
- E2B API key for your team

### Step 1: Create the Python Project

```
my-agent-system/
├── workflows/
│   ├── __init__.py
│   └── agent_workflow.py       # Workflow definition (deterministic)
├── activities/
│   ├── __init__.py
│   ├── llm_activities.py       # LLM API calls
│   └── sandbox_activities.py   # E2B sandbox operations
├── worker.py                   # Worker entry point
├── starter.py                  # Script to trigger workflows
├── Dockerfile
├── k8s/
│   └── worker-deployment.yaml
└── requirements.txt
```

### Step 2: Define Dependencies

```
# requirements.txt
temporalio>=1.9.0
e2b-code-interpreter>=1.0.0
anthropic>=0.40.0           # or openai>=1.0.0
pydantic>=2.0.0
```

### Step 3: Define Data Models

```python
# workflows/__init__.py
from dataclasses import dataclass
from enum import Enum

@dataclass
class AgentTask:
    """Input to the multi-agent workflow."""
    task_description: str
    template_id: str = "base"        # E2B sandbox template
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

@dataclass
class SandboxInfo:
    """Sandbox reference passed between activities."""
    sandbox_id: str
    domain: str
```

### Step 4: Write the Activities

Activities are where the real work happens. They are NOT deterministic (unlike workflows) and can make network calls, use SDKs, etc.

```python
# activities/sandbox_activities.py
import os
from dataclasses import dataclass
from temporalio import activity
from e2b_code_interpreter import Sandbox


E2B_API_KEY = os.environ.get("E2B_API_KEY", "")
# Self-hosted E2B API endpoint (inside the cluster)
E2B_API_URL = os.environ.get("E2B_API_URL", "https://api.yourdomain.com")


@dataclass
class SandboxInfo:
    sandbox_id: str


@activity.defn
async def create_sandbox(template_id: str) -> SandboxInfo:
    """Create a Firecracker VM sandbox."""
    activity.logger.info(f"Creating sandbox with template: {template_id}")

    sandbox = Sandbox(
        template=template_id,
        api_key=E2B_API_KEY,
        api_url=E2B_API_URL,
        timeout=300,  # 5 min TTL
    )

    activity.logger.info(f"Sandbox created: {sandbox.sandbox_id}")
    return SandboxInfo(sandbox_id=sandbox.sandbox_id)


@activity.defn
async def run_code_in_sandbox(sandbox_id: str, code: str) -> str:
    """Execute code inside an existing Firecracker VM."""
    activity.logger.info(f"Executing code in sandbox {sandbox_id}")

    sandbox = Sandbox.connect(
        sandbox_id=sandbox_id,
        api_key=E2B_API_KEY,
        api_url=E2B_API_URL,
    )

    execution = sandbox.run_code(code)

    # Combine stdout, stderr, and any error
    output_parts = []
    if execution.text:
        output_parts.append(execution.text)
    if execution.error:
        output_parts.append(f"ERROR: {execution.error.name}: {execution.error.value}")
        if execution.error.traceback:
            output_parts.append(execution.error.traceback)

    result = "\n".join(output_parts) if output_parts else "(no output)"
    activity.logger.info(f"Execution result ({len(result)} chars)")
    return result


@activity.defn
async def install_packages(sandbox_id: str, packages: list[str]) -> str:
    """Install Python packages inside a sandbox."""
    sandbox = Sandbox.connect(
        sandbox_id=sandbox_id,
        api_key=E2B_API_KEY,
        api_url=E2B_API_URL,
    )

    cmd = f"pip install {' '.join(packages)}"
    execution = sandbox.run_code(
        f"import subprocess; subprocess.run('{cmd}'.split(), capture_output=True, text=True).stdout"
    )
    return execution.text or ""


@activity.defn
async def destroy_sandbox(sandbox_id: str) -> None:
    """Destroy a Firecracker VM sandbox."""
    activity.logger.info(f"Destroying sandbox {sandbox_id}")

    sandbox = Sandbox.connect(
        sandbox_id=sandbox_id,
        api_key=E2B_API_KEY,
        api_url=E2B_API_URL,
    )
    sandbox.kill()
```

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
    # Strip markdown code fences if present
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

### Step 5: Write the Workflow

The workflow is **deterministic** — no direct I/O, no randomness, no current time. All side effects happen in activities.

```python
# workflows/agent_workflow.py
import asyncio
from datetime import timedelta
from temporalio import workflow

# Import activity stubs (these are references, not the actual functions)
with workflow.unsafe.imports_passed_through():
    from activities.sandbox_activities import (
        create_sandbox, run_code_in_sandbox, install_packages, destroy_sandbox,
    )
    from activities.llm_activities import (
        call_planner, call_coder, call_reviewer,
    )
    from workflows import AgentTask, AgentResult, ApprovalSignal


@workflow.defn
class MultiAgentWorkflow:
    """
    Orchestrates a multi-agent pipeline:
      1. Planner agent creates a plan (LLM)
      2. Coder agent writes code (LLM)
      3. Code executes in a Firecracker VM sandbox
      4. Reviewer agent evaluates the result (LLM)
      5. Optionally waits for human approval
      6. Retries if review fails (up to max_iterations)
    """

    def __init__(self):
        self.approval: ApprovalSignal | None = None

    @workflow.signal
    async def approve(self, signal: ApprovalSignal):
        """Receive human approval/rejection signal."""
        self.approval = signal

    @workflow.query
    def current_status(self) -> str:
        """Query the current workflow status."""
        return self._status

    @workflow.run
    async def run(self, task: AgentTask) -> AgentResult:
        self._status = "starting"

        # Activity options: retry up to 3 times with backoff
        activity_opts = workflow.ActivityOptions(
            start_to_close_timeout=timedelta(minutes=5),
            retry_policy=workflow.RetryPolicy(
                initial_interval=timedelta(seconds=1),
                maximum_interval=timedelta(seconds=30),
                maximum_attempts=3,
            ),
        )

        # Longer timeout for sandbox operations
        sandbox_opts = workflow.ActivityOptions(
            start_to_close_timeout=timedelta(minutes=10),
            retry_policy=workflow.RetryPolicy(maximum_attempts=2),
            heartbeat_timeout=timedelta(minutes=2),
        )

        # --- Step 1: Plan ---
        self._status = "planning"
        plan = await workflow.execute_activity(
            call_planner, task.task_description, **activity_opts,
        )

        # --- Step 2: Create sandbox ---
        self._status = "creating_sandbox"
        sandbox = await workflow.execute_activity(
            create_sandbox, task.template_id, **sandbox_opts,
        )

        try:
            code = ""
            output = ""
            review = ""

            for iteration in range(task.max_iterations):
                # --- Step 3: Generate code ---
                self._status = f"coding (iteration {iteration + 1})"
                code = await workflow.execute_activity(
                    call_coder, plan, task.language, **activity_opts,
                )

                # --- Step 4: Execute in sandbox ---
                self._status = f"executing (iteration {iteration + 1})"
                output = await workflow.execute_activity(
                    run_code_in_sandbox,
                    sandbox.sandbox_id,
                    code,
                    **sandbox_opts,
                )

                # --- Step 5: Review ---
                self._status = f"reviewing (iteration {iteration + 1})"
                review = await workflow.execute_activity(
                    call_reviewer,
                    code,
                    output,
                    task.task_description,
                    **activity_opts,
                )

                if "VERDICT: PASS" in review:
                    break

                # If failed, update plan with feedback for next iteration
                plan = f"{plan}\n\nPrevious attempt failed. Reviewer feedback:\n{review}"

            # --- Step 6: Human approval (optional) ---
            if task.require_approval:
                self._status = "waiting_for_approval"

                # Wait up to 24 hours for approval signal (zero CPU during wait)
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

        finally:
            # --- Always destroy sandbox ---
            self._status = "cleanup"
            await workflow.execute_activity(
                destroy_sandbox, sandbox.sandbox_id, **sandbox_opts,
            )

        self._status = "completed"
        return AgentResult(
            plan=plan,
            code=code,
            output=output,
            review=review,
            approved=self.approval.approved if self.approval else True,
        )
```

### Step 6: Write the Worker

The worker is a long-running process that polls Temporal for tasks.

```python
# worker.py
import asyncio
import os
from temporalio.client import Client
from temporalio.worker import Worker

from workflows.agent_workflow import MultiAgentWorkflow
from activities.sandbox_activities import (
    create_sandbox, run_code_in_sandbox, install_packages, destroy_sandbox,
)
from activities.llm_activities import (
    call_planner, call_coder, call_reviewer,
)

TEMPORAL_HOST = os.environ.get(
    "TEMPORAL_HOST",
    "temporal-frontend.temporal.svc.cluster.local:7233",
)
TEMPORAL_NAMESPACE = os.environ.get("TEMPORAL_NAMESPACE", "default")
TASK_QUEUE = os.environ.get("TASK_QUEUE", "agent-tasks")


async def main():
    # Connect to Temporal
    client = await Client.connect(TEMPORAL_HOST, namespace=TEMPORAL_NAMESPACE)

    # Create worker
    worker = Worker(
        client,
        task_queue=TASK_QUEUE,
        workflows=[MultiAgentWorkflow],
        activities=[
            create_sandbox,
            run_code_in_sandbox,
            install_packages,
            destroy_sandbox,
            call_planner,
            call_coder,
            call_reviewer,
        ],
    )

    print(f"Worker started, polling task queue: {TASK_QUEUE}")
    await worker.run()


if __name__ == "__main__":
    asyncio.run(main())
```

### Step 7: Write the Workflow Starter

Use this to trigger workflows from the command line or integrate into your API.

```python
# starter.py
import asyncio
import os
import sys
from temporalio.client import Client
from workflows import AgentTask
from workflows.agent_workflow import MultiAgentWorkflow

TEMPORAL_HOST = os.environ.get(
    "TEMPORAL_HOST",
    "temporal-frontend.temporal.svc.cluster.local:7233",
)
TEMPORAL_NAMESPACE = os.environ.get("TEMPORAL_NAMESPACE", "default")
TASK_QUEUE = os.environ.get("TASK_QUEUE", "agent-tasks")


async def main():
    task_description = sys.argv[1] if len(sys.argv) > 1 else \
        "Write a Python function that finds the longest palindromic substring in a string"

    client = await Client.connect(TEMPORAL_HOST, namespace=TEMPORAL_NAMESPACE)

    # Start the workflow
    handle = await client.start_workflow(
        MultiAgentWorkflow.run,
        AgentTask(
            task_description=task_description,
            template_id="base",
            language="python",
            max_iterations=3,
            require_approval=False,
        ),
        id=f"agent-{task_description[:40].replace(' ', '-').lower()}",
        task_queue=TASK_QUEUE,
    )

    print(f"Workflow started: {handle.id}")
    print(f"View in Web UI: http://localhost:8080/namespaces/default/workflows/{handle.id}")

    # Wait for result
    result = await handle.result()
    print(f"\n{'='*60}")
    print(f"Plan:\n{result.plan}\n")
    print(f"Code:\n{result.code}\n")
    print(f"Output:\n{result.output}\n")
    print(f"Review:\n{result.review}\n")


if __name__ == "__main__":
    asyncio.run(main())
```

### Step 8: Send a Human Approval Signal

When `require_approval=True`, send an approval signal from anywhere:

```python
# approve.py
import asyncio
import sys
from temporalio.client import Client
from workflows import ApprovalSignal

async def main():
    workflow_id = sys.argv[1]
    approved = sys.argv[2].lower() == "true" if len(sys.argv) > 2 else True
    feedback = sys.argv[3] if len(sys.argv) > 3 else ""

    client = await Client.connect(
        "temporal-frontend.temporal.svc.cluster.local:7233"
    )

    handle = client.get_workflow_handle(workflow_id)
    await handle.signal(
        "approve",
        ApprovalSignal(approved=approved, feedback=feedback),
    )
    print(f"Signal sent to {workflow_id}: approved={approved}")

if __name__ == "__main__":
    asyncio.run(main())
```

### Step 9: Containerize

```dockerfile
# Dockerfile
FROM python:3.12-slim

WORKDIR /app

COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

COPY . .

CMD ["python", "worker.py"]
```

Build and push to ECR:

```bash
# Get ECR login
ECR_URL=$(terraform -chdir=iac/provider-aws output -raw core_ecr_repository_url)
REGISTRY=$(echo $ECR_URL | cut -d/ -f1)
aws ecr get-login-password --region $AWS_REGION | docker login --username AWS --password-stdin $REGISTRY

# Create ECR repo for the worker
aws ecr create-repository --repository-name e2b-agent-worker --region $AWS_REGION

# Build and push
WORKER_REPO="$REGISTRY/e2b-agent-worker"
docker build -t $WORKER_REPO:latest .
docker push $WORKER_REPO:latest
```

### Step 10: Deploy the Worker to EKS

```yaml
# k8s/worker-deployment.yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: agent-worker
  namespace: e2b
  labels:
    app: agent-worker
spec:
  replicas: 2
  selector:
    matchLabels:
      app: agent-worker
  template:
    metadata:
      labels:
        app: agent-worker
    spec:
      nodeSelector:
        e2b.dev/node-pool: system
      containers:
        - name: worker
          image: <REGISTRY>/e2b-agent-worker:latest  # Replace with your ECR URL
          env:
            - name: TEMPORAL_HOST
              value: "temporal-frontend.temporal.svc.cluster.local:7233"
            - name: TEMPORAL_NAMESPACE
              value: "default"
            - name: TASK_QUEUE
              value: "agent-tasks"
            - name: E2B_API_URL
              value: "http://api.e2b.svc.cluster.local:50001"
            - name: E2B_API_KEY
              valueFrom:
                secretKeyRef:
                  name: agent-worker-secrets
                  key: e2b-api-key
            - name: ANTHROPIC_API_KEY
              valueFrom:
                secretKeyRef:
                  name: agent-worker-secrets
                  key: anthropic-api-key
          resources:
            requests:
              cpu: "250m"
              memory: "256Mi"
            limits:
              cpu: "500m"
              memory: "512Mi"
---
# Create secrets first:
# kubectl create secret generic agent-worker-secrets -n e2b \
#   --from-literal=e2b-api-key=YOUR_KEY \
#   --from-literal=anthropic-api-key=YOUR_KEY
```

Deploy:

```bash
# Create the secrets
kubectl create secret generic agent-worker-secrets -n e2b \
  --from-literal=e2b-api-key="$E2B_API_KEY" \
  --from-literal=anthropic-api-key="$ANTHROPIC_API_KEY"

# Deploy the worker
kubectl apply -f k8s/worker-deployment.yaml

# Verify worker is running and connected
kubectl logs -f deploy/agent-worker -n e2b
```

### Step 11: Test End-to-End

```bash
# Option A: Run starter locally (with port-forward)
kubectl port-forward svc/temporal-frontend -n temporal 7233:7233 &
TEMPORAL_HOST=localhost:7233 python starter.py "Calculate the first 20 Fibonacci numbers"

# Option B: Run starter as a K8s job
kubectl run agent-starter --rm -it --image=<REGISTRY>/e2b-agent-worker:latest \
  -n e2b --env="TEMPORAL_HOST=temporal-frontend.temporal.svc.cluster.local:7233" \
  -- python starter.py "Calculate the first 20 Fibonacci numbers"

# Watch workflow progress in Web UI
kubectl port-forward svc/temporal-web -n temporal 8080:8080
# Open http://localhost:8080
```

### Step 12: Observe

```bash
# Watch workflow in Temporal Web UI
kubectl port-forward svc/temporal-web -n temporal 8080:8080
# http://localhost:8080 → click on workflow → see each activity's input/output

# Query workflow status programmatically
kubectl run query --rm -it --image=<REGISTRY>/e2b-agent-worker:latest \
  -n e2b -- python -c "
import asyncio
from temporalio.client import Client
from workflows.agent_workflow import MultiAgentWorkflow

async def main():
    client = await Client.connect('temporal-frontend.temporal.svc.cluster.local:7233')
    handle = client.get_workflow_handle('YOUR_WORKFLOW_ID')
    status = await handle.query(MultiAgentWorkflow.current_status)
    print(f'Status: {status}')

asyncio.run(main())
"

# List running workflows
kubectl exec -it deploy/temporal-admintools -n temporal -- \
  tctl workflow list --open
```

### What Happens When Things Fail

This is why Temporal exists — every failure mode is handled automatically:

| Failure | What Temporal Does |
|---------|-------------------|
| Worker pod crashes mid-activity | Activity times out, Temporal retries on another worker |
| LLM API returns 500 | Activity retry policy kicks in (3 attempts, exponential backoff) |
| Sandbox creation fails | Activity retries with a new sandbox |
| Worker pod killed during sandbox execution | Sandbox TTL auto-expires; new worker retries the activity |
| Entire EKS node goes down | Worker rescheduled on new node, workflow resumes from last checkpoint |
| Human doesn't approve within 24h | Workflow times out with `ApplicationError` (zero CPU consumed while waiting) |
| You deploy a new worker version | Old activities complete on old workers; new activities route to new workers |

Every activity input/output is persisted in Aurora. You can inspect the full execution history in the Web UI — including exactly what code was generated, what the sandbox output was, and what the LLM reviewer said.

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
