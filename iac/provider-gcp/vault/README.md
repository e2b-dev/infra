# HashiCorp Vault Deployment

This package deploys HashiCorp Vault 1.14.8 on Nomad with GCS as the storage backend.

## Overview

Vault is deployed as a highly available (TODO) cluster with the following features:
- **Version**: 1.14.8 (last before BSL change)
- **Storage Backend**: GCS
- **High Availability**: 1 server node (TODO: increase to at least 3 for production)
- **Auto-Unseal**: GCP Cloud KMS integration for automatic unsealing
- **Service Discovery**: Registered with Consul
- **Authentication**: Pre-configured AppRoles for API and Orchestrator services
- **Secrets Engine**: KV v2 enabled at `secret/`
- **Ports**:
  - API: 8200
  - Cluster: 8201

## Deployment

```bash
cd packages/vault/scripts

# Initialize Vault with GCP KMS auto-unseal and configure AppRoles
./init-vault.sh \
  --project <gcp-project-id> \
  --prefix <environment-prefix> \
  --vault-addr http://vault.service.consul:8200
```

This script will:
- Store root token in GCP Secret Manager
- Configure GCP KMS auto-unseal (Vault automatically unseals on restart)
- Enable KV v2 secrets engine at `secret/`
- Create AppRoles for services:
  - **API Service** (`api-service`): Write and delete permissions
  - **Orchestrator Service** (`orchestrator-service`): Read-only permissions
- Save AppRole credentials to GCP Secret Manager

**Note**: The script is idempotent and can be safely re-run. It will skip creating AppRoles if they already exist.

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
