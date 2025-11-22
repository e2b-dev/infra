# Self-hosting E2B on Google Cloud

## Prerequisites

**Tools**

- [Packer](https://developer.hashicorp.com/packer/tutorials/docker-get-started/get-started-install-cli#installing-packer)
  - Used for building the disk image of the orchestrator client and server

- [Terraform](https://developer.hashicorp.com/terraform/tutorials/aws-get-started/install-cli) (v1.5.x)
  - We ask for v1.5.x because starting from v1.6 Terraform [switched](https://github.com/hashicorp/terraform/commit/b145fbcaadf0fa7d0e7040eac641d9aef2a26433) their license from Mozilla Public License to Business Source License.
  - The last version of Terraform that supports Mozilla Public License is **v1.5.7**
    - Binaries are available [here](https://developer.hashicorp.com/terraform/install/versions#binary-downloads)
    - You can also install it via [tfenv](https://github.com/tfutils/tfenv)
      ```sh
      brew install tfenv
      tfenv install 1.5.7
      tfenv use 1.5.7
      ```

- [Google Cloud CLI](https://cloud.google.com/sdk/docs/install)
  - Used for managing the infrastructure on Google Cloud
  - Be sure to authenticate:
    ```sh
    gcloud auth login
    gcloud auth application-default login
    ```

- [Golang](https://go.dev/doc/install)

- [Docker](https://docs.docker.com/engine/install/)


**Accounts**

- Cloudflare account
- Domain on Cloudflare
- GCP account + project
- PostgreSQL database (Supabase's DB only supported for now)

**Optional**

Recommended for monitoring and logging
- Grafana Account & Stack (see Step 15 for detailed notes)
- Posthog Account

## Steps

Check if you can use config for terraform state management

1. Go to `console.cloud.google.com` and create a new GCP project
    > Make sure your Quota allows you to have at least 2500G for `Persistent Disk SSD (GB)` and at least 24 for `CPUs`
2. Create `.env.prod`, `.env.staging`, or `.env.dev` from [`.env.template`](.env.template). You can pick any of them. Make sure to fill in the values. All are required if not specified otherwise.
    > Get Postgres database connection string from your database, e.g. [from Supabase](https://supabase.com/docs/guides/database/connecting-to-postgres#direct-connection): Create a new project in Supabase and go to your project in Supabase -> Settings -> Database -> Connection Strings -> Postgres -> Direct
    
    > Your Postgres database needs to have enabled IPv4 access. You can do that in Connect screen
3. Run `make switch-env ENV={prod,staging,dev}` to start using your env
4. Run `make login` (for provider=gcp)
5. Run `make init`. If this errors, run it a second time--it's due to a race condition on Terraform enabling API access for the various GCP services; this can take several seconds. A full list of services that will be enabled for API access:
   - [Secret Manager API](https://console.cloud.google.com/apis/library/secretmanager.googleapis.com)
   - [Certificate Manager API](https://console.cloud.google.com/apis/library/certificatemanager.googleapis.com)
   - [Compute Engine API](https://console.cloud.google.com/apis/library/compute.googleapis.com)
   - [Artifact Registry API](https://console.cloud.google.com/apis/library/artifactregistry.googleapis.com)
   - [OS Config API](https://console.cloud.google.com/apis/library/osconfig.googleapis.com)
   - [Stackdriver Monitoring API](https://console.cloud.google.com/apis/library/monitoring.googleapis.com)
   - [Stackdriver Logging API](https://console.cloud.google.com/apis/library/logging.googleapis.com)
6. Run `make build-and-upload`
7. Run `make copy-public-builds`. This will copy kernel and rootfs builds for Firecracker to your bucket. You can [build your own](#building-firecracker-and-uffd-from-source) kernel and Firecracker roots.
8. Secrets are created and stored in GCP Secrets Manager. Once created, that is the source of truth--you will need to update values there to make changes. Create a secret value for the following secrets:
  - e2b-cloudflare-api-token
      > Get Cloudflare API Token: go to the [Cloudflare dashboard](https://dash.cloudflare.com/) -> Manage Account -> Account API Tokens -> Create Token -> Edit Zone DNS -> in "Zone Resources" select your domain and generate the token
  - Posthog API keys for monitoring (optional)
9. Run `make plan-without-jobs` and then `make apply`
10. Fill out the following secret in the GCP Secrets Manager:
  - e2b-supabase-jwt-secrets (optional / required to self-host the [E2B dashboard](https://github.com/e2b-dev/dashboard))
      > Get Supabase JWT Secret: go to the [Supabase dashboard](https://supabase.com/dashboard) -> Select your Project -> Project Settings -> Data API -> JWT Settings
  - e2b-postgres-connection-string
11. Run `make plan` and then `make apply`. Note: This will work after the TLS certificates was issued. It can take some time; you can check the status in the Google Cloud Console
12. Setup data in the cluster by following one of the two 
    - `make prep-cluster` in `packages/shared` to create an initial user, etc. (You need to be logged in via [`e2b` CLI](https://www.npmjs.com/package/@e2b/cli)). It will create a user with same information (access token, api key, etc.) as you have in E2B. 
    - You can also create a user in database, it will automatically also create a team, an API key and an access token. You will need to build template(s) for your cluster. Use [`e2b` CLI](https://www.npmjs.com/package/@e2b/cli?activetab=versions)) and run `E2B_DOMAIN=<your-domain> e2b template build`.


### Interacting with the cluster

#### SDK
When using SDK pass domain when creating new `Sandbox` in JS/TS SDK
```js
import { Sandbox } from "@e2b/sdk";

const sandbox = new Sandbox({
  domain: "<your-domain>",
});
```

or in Python SDK

```python
from e2b import Sandbox

sandbox = Sandbox(domain="<your-domain>")
```

#### CLI
When using CLI you can pass domain as well
```sh
E2B_DOMAIN=<your-domain> e2b <command>
```

#### Monitoring and logging jobs

To access the nomad web UI, go to https://nomad.<your-domain.com>. Go to sign in, and when prompted for an API token, you can find this in GCP Secrets Manager. From here, you can see nomad jobs and tasks for both client and server, including logging.

To update jobs running in the cluster look inside packages/nomad for config files. This can be useful for setting your logging and monitoring agents.

### Troubleshooting

If any problems arise, open [a Github Issue on the repo](https://github.com/e2b-dev/infra/issues) and we'll look into it.

---

### Building Firecracker and UFFD from source

E2B is using [Firecracker](https://github.com/firecracker-microvm/firecracker) for Sandboxes.
You can build your own kernel and Firecracker version from source by running `make build-and-upload-fc-components`

- Note: This needs to be done on a Linux machine due to case-sensitive requirements for the file system--you'll error out during the automated git section with a complaint about unsaved changes. Kernel and versions could alternatively be sourced elsewhere.

### Make commands cheat sheet

- `make init` - setup the terraform environment
- `make plan` - plans the terraform changes
- `make apply` - applies the terraform changes, you have to run `make plan` before this one
- `make plan-without-jobs` - plans the terraform changes without provisioning nomad jobs
- `make plan-only-jobs` - plans the terraform changes only for provisioning nomad jobs
- `make destroy` - destroys the cluster
- `make version` - increments the repo version
- `make build-and-upload` - builds and uploads the docker images, binaries, and cluster disk image
- `make copy-public-builds` - copies the old envd binary, kernels, and firecracker versions from the public bucket to your bucket
- `make migrate` - runs the migrations for your database
- `make login` - logs in to the selected provider (gcp will invoke gcloud; linux has no login)
- `make switch-env ENV={prod,staging,dev}` - switches the environment
- `make import TARGET={resource} ID={resource_id}` - imports the already created resources into the terraform state
- `make setup-ssh` - sets up the ssh key for the environment (useful for remote-debugging)
- `make connect-orchestrator` - establish the ssh connection to the remote orchestrator (for testing API locally)

---

## Google Cloud Troubleshooting
**Quotas not available** 

If you can't find the quota in `All Quotas` in GCP's Console, then create and delete a dummy VM before proceeding to step 2 in self-deploy guide. This will create additional quotas and policies in GCP 
```
gcloud compute instances create dummy-init   --project=YOUR-PROJECT-ID   --zone=YOUR-ZONE   --machine-type=e2-medium   --boot-disk-type=pd-ssd   --no-address
```
Wait a minute and destroy the VM:
```
gcloud compute instances delete dummy-init --zone=YOUR-ZONE --quiet
```
Now, you should see the right quota options in `All Quotas` and be able to request the correct size. 

---

## Self-hosting E2B on Linux (Bare Metal)

### Prerequisites

- Terraform (v1.5.x)
  - v1.5.7 recommended (license compatibility)
- SSH access to all machines
  - Each node must be reachable via SSH with a user and private key
- Supported OS
  - Ubuntu/Debian-based distributions with `systemd`
- Docker
  - Terraform will install Docker if missing

### Accounts and Secrets

- PostgreSQL database connection string
- Optional: Posthog API key, LaunchDarkly API key
- Optional: Redis secure cluster URL and TLS CA

### Steps

1. Create `.env.prod`, `.env.staging`, or `.env.dev` from [`.env.template`](.env.template)
   - Set `PROVIDER=linux`
   - Set `DATACENTER`, e.g. `dc1`
   - Set `NOMAD_ADDRESS`, e.g. `http://localhost:4646` (per node)
   - Optionally set `NOMAD_ACL_TOKEN` and `CONSUL_ACL_TOKEN`
   - Define bare-metal nodes as JSON strings:
     - `SERVERS_JSON='[{"host":"10.0.0.10","ssh_user":"ubuntu","ssh_private_key_path":"/home/me/.ssh/id_rsa","node_pool":"servers"}]'`
     - `CLIENTS_JSON='[{"host":"10.0.0.20","ssh_user":"ubuntu","ssh_private_key_path":"/home/me/.ssh/id_rsa","node_pool":"api"}]'`
   - Fill port objects (JSON), images, artifacts and application secrets per the template comments
2. Run `make switch-env ENV={prod,staging,dev}`
3. Run `make init`
4. Build and upload images and artifacts
   - Set a Docker registry prefix (optional, enables auto image names)
     ```sh
     # Example: local registry on 192.168.3.4
     echo dev > .last_used_env
     # in .env.dev
     DOCKER_IMAGE_PREFIX=192.168.3.4:5000/e2b-orchestration
     ```
   - 使用全局 Makefile 构建并推送镜像与二进制
     ```sh
     make build-and-upload-linux
     ```
   - Upload orchestrator/template-manager binaries for HTTP artifact URLs
     ```sh
     # Ensure in .env.dev you set:
     # ARTIFACT_HTTP_HOST, ARTIFACT_HTTP_USER, ARTIFACT_HTTP_DIR, ARTIFACT_HTTP_SSH_KEY, ARTIFACT_HTTP_PORT
     # And artifact URLs consumed by Nomad jobs:
     # ORCHESTRATOR_ARTIFACT_URL=http://<host>:<port>/orchestrator
     # TEMPLATE_MANAGER_ARTIFACT_URL=http://<host>:<port>/template-manager

     # 已包含于 make build-and-upload-linux
     ```
   - Notes
     - The upload scripts auto-select backend:
       - `PROVIDER=gcp` → uploads to GCS via `gsutil`
       - `PROVIDER=linux` → uploads via `scp` to `ARTIFACT_HTTP_*` location
     - Serve artifacts over HTTP (e.g., nginx/caddy) to match the URLs used by Nomad jobs
4. Plan/apply base resources (without Nomad jobs)
   - `make plan-without-jobs`
   - `make apply`
5. Plan/apply Nomad jobs
   - `make plan-only-jobs`
   - `make apply`
6. Verify services
   - Nomad UI: `http://<server_ip>:4646` (Nomad bound to `0.0.0.0`)
   - Consul UI: `http://<server_ip>:8500` (if UI enabled)
   - Consul DNS: system resolver is configured to use `127.0.0.1:8600`

### What Terraform does on bare metal

- Configures Consul (`/etc/consul.d/consul.json`) and Nomad (`/etc/nomad.d/nomad.json`) via SSH
- Starts and enables `consul` and `nomad` services via `systemd`
- Optionally enables Consul ACL if `CONSUL_ACL_TOKEN` is set
- Configures `systemd-resolved` to resolve via Consul DNS (`127.0.0.1:8600`)
- Provisions Nomad jobs for API, ingress, orchestrator, template-manager, Loki, and Otel Collector

### Variables to pay attention to

- Node pools: `API_NODE_POOL`, `ORCHESTRATOR_NODE_POOL`, `BUILDER_NODE_POOL`
- Ports (JSON strings): `API_PORT`, `INGRESS_PORT`, `EDGE_API_PORT`, `EDGE_PROXY_PORT`, `LOGS_PROXY_PORT`, `LOKI_SERVICE_PORT`, `LOGS_HEALTH_PROXY_PORT`
- Artifacts: `ORCHESTRATOR_ARTIFACT_URL`, `TEMPLATE_MANAGER_ARTIFACT_URL`
- Observability: `OTEL_COLLECTOR_GRPC_PORT`, `OTEL_COLLECTOR_RESOURCES_*`, `LOKI_RESOURCES_*`
- Secrets/config: `API_SECRET`, `EDGE_API_SECRET`, `API_ADMIN_TOKEN`, `SUPABASE_JWT_SECRETS`, `POSTHOG_API_KEY`, `LAUNCH_DARKLY_API_KEY`
- Redis: `REDIS_URL`, `REDIS_SECURE_CLUSTER_URL`, `REDIS_TLS_CA_BASE64`
- Buckets/cache: `TEMPLATE_BUCKET_NAME`, `BUILD_CACHE_BUCKET_NAME`, `SHARED_CHUNK_CACHE_PATH`
- Sandbox/networking: `ENVD_TIMEOUT`, `ALLOW_SANDBOX_INTERNET`
- ClickHouse (optional): set `CLICKHOUSE_*` variables; enable via Terraform by setting `clickhouse_server_count > 0`

### Troubleshooting (Linux)

- SSH errors: verify `ssh_user`, `ssh_private_key_path`, and host connectivity
- Consul/Nomad not starting: check `/var/log/syslog` and `systemctl status {consul,nomad}`
- DNS resolution: ensure `/etc/systemd/resolved.conf.d/consul.conf` exists and `systemd-resolved` is running
- Jobs not visible: check Nomad ACL token (`NOMAD_ACL_TOKEN`) and Nomad address in `.env`
