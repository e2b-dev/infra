# HashiCorp Vault Deployment

This package deploys HashiCorp Vault 1.20.3 on Nomad with Google Spanner as the storage backend.

## Overview

Vault is deployed as a highly available cluster with the following features:
- **Version**: 1.20.3
- **Storage Backend**: GCS
- **High Availability**: 3 instances, 1 is always the active node (reachable through `vault-leader` service)
- **TLS**: Self-signed, created through TLS provider + stored in GCP Secret Manager
- **Auto-Unseal**: GCP Cloud KMS integration for automatic unsealing
- **Service Discovery**: Registered with Consul
- **Authentication**: Pre-configured AppRoles for API and Orchestrator services
- **Secrets Engine**: KV v2 enabled at `secret/`
- **Ports**:
  - API: 8200
  - Cluster: 8201


## Google KMS

The keys for unsealing are stored in Google Cloud KMS and rotated every 90 days. DO NOT DELETE OLD KEY VERSIONS!

### 3. AppRole Authentication

The initialization script automatically creates two AppRoles:

#### API Service AppRole
- **Role**: `api-service`
- **Permissions**: Create, update, delete operations on secrets
- **Credentials**: Stored in `${prefix}vault-api-approle` secret
- **Policy**:
  ```hcl
  path "secret/data/*" {
    capabilities = ["create", "update", "delete"]
  }

  path "secret/metadata/*" {
    capabilities = ["create", "update", "delete", "read", "list"]
  }
  ```

#### Orchestrator Service AppRole
- **Role**: `orchestrator-service`
- **Permissions**: Full access to secrets (needs write access for certificates and read for injection)
- **Credentials**: Stored in `${prefix}vault-orchestrator-approle` secret
- **Policy**:
  ```hcl
  path "secret/data/*" {
    capabilities = ["create", "read", "update", "delete"]
  }

  path "secret/metadata/*" {
    capabilities = ["create", "update", "delete", "read", "list"]
  }
  ```




## Scaling

Hashicorp vault is active-passive (1 active node, n-1 standby) so adding more nodes does not help. You can either:

1) increase spanner processing units (vault_spanner_processing_units)

2) increase mhz of vault tasks (vault_resources -> cpu)

Writes/s will most likely be the main bottleneck. Reads are quite performant, even on the lowest 100 PU spanner tier 10k reads/s should be possible.

In my limited synthetic testing, I could reach sustained 1000 writes/s with ok performance by scaling PU and MHZ of the vault task.

```
op                    count  rate         throughput  mean         95th%         99th%         successRatio
static_secret_writes  30000  1000.038088  996.919883  80.104708ms  177.918346ms  258.044012ms  100.00%
```


## Running benchmarks

The easiest way to benchmark the vault is to ssh into the node that runs a vault node, active or standby.

Install official benchmark tool:
```bash
wget https://releases.hashicorp.com/vault-benchmark/0.3.0/vault-benchmark_0.3.0_linux_amd64.zip
unzip vault-benchmark_0.3.0_linux_amd64.zip
```

Create `vault-benchmark-config.hcl`

```hcl
vault_addr = "https://localhost:8200"
vault_token = "insert_root_token_or_token_with_correct_permissions"
duration = "15s"
cleanup = false

test "kvv2_write" "static_secret_writes" {
  weight = 100
  config {
    numkvs = 1000
    kvsize = 100
  }
}
```

You can get the root token in `e2b-vault-root-key` in Google Secret Manager. You will also need to create `ca.pem` which is the self-signed cert that you can find in `e2b-vault-tls-ca` in Google Secret manager.

```bash
./vault-benchmark run -config=./vault-benchmark-config.hcl -ca_pem_file=ca.pem -debug -rps=50 -worker=10
```

Play around with `worker`, `rps` and `numkvs`.

You can also add reads to the benchmark:

```hcl
vault_addr = "https://localhost:8200"
vault_token = "insert_root_token_or_token_with_correct_permissions"
duration = "15s"
cleanup = false

test "kvv2_write" "static_secret_writes" {
  weight = 50
  config {
    numkvs = 1000
    kvsize = 100
  }
}

test "kvv2_read" "static_secret_reads" {
  weight = 50
  config {
    numkvs = 1000
    kvsize = 100
  }
}
```



## Notes

At first we used GCS buckets as backend, but the 1 write/s per object quota is a dealbreaker (breaks at 25 secret writes/s)