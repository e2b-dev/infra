# Nomad NodePool APM Plugin

A custom APM (Application Performance Monitoring) plugin for the [Nomad Autoscaler](https://github.com/hashicorp/nomad-autoscaler) that provides the count of nodes in a Nomad node pool.

## Purpose

This plugin enables service jobs to scale their count based on the number of nodes in a specific node pool, effectively replicating the behavior of system jobs while benefiting from proper rolling update support.

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
apm "nomad-nodepool" {
  driver = "nomad-nodepool"
  config = {
    nomad_address = "http://localhost:4646"  # Optional, uses NOMAD_ADDR env var
    nomad_token   = "your-token"             # Optional, uses NOMAD_TOKEN env var
    nomad_region  = "global"                 # Optional
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

    check "match_node_count" {
      source = "nomad-nodepool"
      query  = "build"  # Node pool name

      strategy "pass-through" {}  # Use node count directly as desired count
    }
  }
}
```

## How It Works

1. The plugin queries the Nomad API to list nodes filtered by the specified node pool
2. It counts only nodes that are:
   - Status: `ready`
   - Scheduling eligibility: `eligible`
3. Returns the count as a metric for the autoscaler to use
4. With the `pass-through` strategy, this count becomes the desired number of allocations

## Query Format

The `query` parameter should be the name of the node pool you want to count nodes from.

Example: `query = "build"` will count all ready, eligible nodes in the "build" node pool.

## Configuration Options

| Option | Description | Default |
|--------|-------------|---------|
| `nomad_address` | Nomad server address | `NOMAD_ADDR` env var or `http://127.0.0.1:4646` |
| `nomad_token` | Nomad ACL token | `NOMAD_TOKEN` env var |
| `nomad_region` | Nomad region | Default region |
| `nomad_namespace` | Nomad namespace | Default namespace |

