# Nomad Node Pool Autoscaler Plugins

Custom plugins for the [Nomad Autoscaler](https://github.com/hashicorp/nomad-autoscaler) that keep a service-job allocation count aligned with the number of nodes in a Nomad node pool.

## Purpose

The package builds two plugins because Nomad Autoscaler supports only one plugin type per external binary:

- `nomad-nodepool-apm` reports the number of ready nodes in a pool.
- `nomad-deployment-aware-target` fails an active deployment before applying the requested task-group count.

Together they let service jobs replicate system-job placement while retaining rolling updates. The deployment-aware target is used by the `template-manager` and `orchestrator-ee` service jobs. When the task-group count must change, scaling intentionally abandons a conflicting in-progress rollout (Nomad rejects scaling while a deployment is active); Nomad records that deployment as `failed`, rather than `cancelled`. The rollout spawned by the scale itself is left to run, and no deployment is touched when the count already matches. The target requires `auto_revert = false` on the scaled group (otherwise failing a deployment would restore an older job version and ping-pong); it refuses to scale groups with `auto_revert = true` and returns an error on every attempt.

## Usage

### Building

```bash
make build
```

### Uploading to GCS

```bash
GCP_PROJECT_ID=your-project-id make build-and-upload
```

### Configuration

In your Nomad Autoscaler configuration:

```hcl
apm "nomad-nodepool-apm" {
  driver = "nomad-nodepool-apm"
  config = {
    nomad_address = "http://localhost:4646"  # Optional, uses NOMAD_ADDR env var
    nomad_token   = "your-token"             # Optional, uses NOMAD_TOKEN env var
    nomad_region  = "global"                 # Optional
  }
}

target "nomad-deployment-aware-target" {
  driver = "nomad-deployment-aware-target"
  config = {
    nomad_address = "http://localhost:4646"
    nomad_token   = "your-token"
    nomad_region  = "global"
  }
}
```

### In Job Scaling Policy

```hcl
scaling {
  enabled = true
  min     = 1
  max     = 50

  policy {
    evaluation_interval = "30s"
    cooldown            = "2m"

    target "nomad-deployment-aware-target" {}

    check "match_node_count" {
      source = "nomad-nodepool-apm"
      query  = "orchestrator"  # Node pool name

      strategy "pass-through" {}  # Use node count directly as desired count
    }
  }
}
```

## How It Works

1. The plugin queries the Nomad API to list nodes filtered by the specified node pool
2. It counts only nodes that are:
    - Status: `ready`
3. Returns the count as a metric for the autoscaler to use
4. With the `pass-through` strategy, this count becomes the desired number of allocations
5. The target serializes scaling per namespaced job and is a no-op when the durable count already matches
6. When the count must change it fails a conflicting active deployment, rereads the job, and scales with the current job modify index
7. It verifies the final durable count; the rollout spawned by the scale proceeds normally
8. Concurrent job or deployment changes are retried from fresh state with a bounded attempt count

Dry-run actions do not fail deployments or write task-group counts.

## Query Format

The `query` parameter should be the name of the node pool you want to count nodes from.

Example: `query = "orchestrator"` counts all ready nodes in the `orchestrator` node pool.

## Configuration Options

| Option | Description | Default |
|--------|-------------|---------|
| `nomad_address` | Nomad server address | `NOMAD_ADDR` env var or `http://127.0.0.1:4646` |
| `nomad_token` | Nomad ACL token | `NOMAD_TOKEN` env var |
| `nomad_region` | Nomad region | Default region |
| `nomad_namespace` | Nomad namespace | Default namespace |
