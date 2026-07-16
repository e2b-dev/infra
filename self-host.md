# Self-hosting E2B

## Prerequisites

**Tools**

- [Packer](https://developer.hashicorp.com/packer/tutorials/docker-get-started/get-started-install-cli#installing-packer)
  - Used for building the disk image of the orchestrator client and server

- [Terraform](https://developer.hashicorp.com/terraform/tutorials/aws-get-started/install-cli) (v1.7.5)
    - Binaries are available [here](https://developer.hashicorp.com/terraform/install/versions#binary-downloads)
  - You can also install it via [tfenv](https://github.com/tfutils/tfenv)
    ```sh
    brew install tfenv
    tfenv install 1.7.5
    tfenv use 1.7.5
    ```

- [Golang](https://go.dev/doc/install)

- [Docker](https://docs.docker.com/engine/install/)

- [Docker Buildx](https://github.com/docker/buildx#installing) plugin
  - Required by `make build-and-upload` targets. Bundled with Docker Desktop; must be installed separately if you use Docker Engine/CLI without Desktop.
  - If `docker buildx version` errors after install, Docker can't find the plugin. Add its directory (e.g. `/opt/homebrew/lib/docker/cli-plugins` for Homebrew) to `cliPluginsExtraDirs` in `~/.docker/config.json`.

- [NPM](https://docs.npmjs.com/downloading-and-installing-node-js-and-npm)

**Accounts**

- Cloudflare account
- Domain on Cloudflare
- PostgreSQL database

**Optional**

Recommended for monitoring and logging
- Grafana Account & Stack
- Posthog Account

---

## Google Cloud

### Additional Prerequisites

- [Google Cloud CLI](https://cloud.google.com/sdk/docs/install)
  - Used for managing the infrastructure on Google Cloud
  - Be sure to authenticate:
    ```sh
    gcloud auth login --update-adc
    ```
- GCP account + project

### Steps

Check if you can use config for terraform state management

1. Go to `console.cloud.google.com` and create a new GCP project
    > Make sure your Quota allows you to have at least 2500G for `Persistent Disk SSD (GB)` and at least 24 for `CPUs`
2. Create `.env.prod`, `.env.staging`, or `.env.dev` from [`.env.gcp.template`](.env.gcp.template). You can pick any of them. Make sure to fill in the values. All are required if not specified otherwise.
    > Get the Postgres database connection string from your database. It needs to be IPv4 compatible.
3. Run `make set-env ENV={prod,staging,dev}` to start using your env
4. Run `make provider-login` to login to `gcloud`
5. Run `make init`. If this errors, run it a second time--it's due to a race condition on Terraform enabling API access for the various GCP services; this can take several seconds. A full list of services that will be enabled for API access:
   - [Secret Manager API](https://console.cloud.google.com/apis/library/secretmanager.googleapis.com)
   - [Certificate Manager API](https://console.cloud.google.com/apis/library/certificatemanager.googleapis.com)
   - [Compute Engine API](https://console.cloud.google.com/apis/library/compute.googleapis.com)
   - [Artifact Registry API](https://console.cloud.google.com/apis/library/artifactregistry.googleapis.com)
   - [OS Config API](https://console.cloud.google.com/apis/library/osconfig.googleapis.com)
   - [Stackdriver Monitoring API](https://console.cloud.google.com/apis/library/monitoring.googleapis.com)
   - [Stackdriver Logging API](https://console.cloud.google.com/apis/library/logging.googleapis.com)
   - [Filestore API](https://console.cloud.google.com/apis/library/file.googleapis.com)
6. Run `make build-and-upload`
7. Run `make copy-public-builds`. This will copy kernels, Firecracker versions, and busybox to your bucket. You can [build your own](#building-firecracker-and-uffd-from-source) kernel and Firecracker versions from source.
8. For following secrets terraform creates only an empty secret containers in GCP Secrets Manager. You need to add a **secret version** with the actual value. Go to [GCP Secrets Manager](https://console.cloud.google.com/security/secret-manager), click on the secret, then click "New Version" to add the value for the following secrets:
  - e2b-cloudflare-api-token
      > Get Cloudflare API Token: go to the [Cloudflare dashboard](https://dash.cloudflare.com/) -> Manage Account -> Account API Tokens -> Create Token -> Edit Zone DNS -> in "Zone Resources" select your domain and generate the token
  - e2b-postgres-connection-string (**required**)
  - e2b-posthog-api-key (optional, for monitoring)
9. Run `make plan-without-jobs` and then `make apply`
10. Run `make plan` and then `make apply`. Note: This will work after the TLS certificates was issued. It can take some time; you can check the status in the Google Cloud Console. Database migrations run automatically via the API's db-migrator task.
11. Setup data in the cluster by running `make prep-cluster` to create an initial user, team, and build a base template.
  - You can also run `make seed-db` to create more users and teams.

### GCP Troubleshooting

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

## AWS

### Additional Prerequisites

- [AWS CLI](https://docs.aws.amazon.com/cli/latest/userguide/getting-started-install.html)
  - Used for managing the infrastructure on AWS
  - Be sure to configure a profile:
    ```sh
    aws configure --profile <your-profile>
    ```
- AWS account

### Steps

1. Create `.env.prod`, `.env.staging`, or `.env.dev` from [`.env.aws.template`](.env.aws.template). Make sure to fill in the AWS-specific values:
    - `PROVIDER=aws`
    - `AWS_PROFILE` - your AWS CLI profile name
    - `AWS_ACCOUNT_ID` - your AWS account ID
    - `AWS_REGION` - the AWS region to deploy to (must support nested virtualization for Firecracker)
    - `PREFIX` - name prefix for all resources (e.g. `e2b-`)
    - `DOMAIN_NAME` - your domain managed by Cloudflare
    - `TERRAFORM_ENVIRONMENT` - one of `prod`, `staging`, `dev`
2. Run `make set-env ENV={prod,staging,dev}` to start using your env
3. Run `make provider-login` to authenticate with AWS ECR
4. Run `make init`. This creates:
    - S3 bucket for Terraform state
    - VPC, subnets, and networking
    - ECR repositories for container images
    - S3 buckets for templates, kernels, builds, and backups
    - Secrets in AWS Secrets Manager (with placeholder values)
    - Cloudflare DNS records and TLS certificates
5. Update the following secrets in [AWS Secrets Manager](https://console.aws.amazon.com/secretsmanager) with actual values:
    - `{prefix}cloudflare` - JSON with `TOKEN` key
        > Get Cloudflare API Token: go to the [Cloudflare dashboard](https://dash.cloudflare.com/) -> Manage Account -> Account API Tokens -> Create Token -> Edit Zone DNS -> in "Zone Resources" select your domain and generate the token
    - `{prefix}postgres-connection-string` - your PostgreSQL connection string (**required**)
    - `{prefix}grafana` - JSON with `API_KEY`, `OTLP_URL`, `OTEL_COLLECTOR_TOKEN`, `USERNAME` keys (optional, for monitoring)
    - `{prefix}launch-darkly-api-key` - LaunchDarkly SDK key (optional, for feature flags)
6. Build the Packer AMI for cluster nodes (a single shared AMI used by all node types):
    ```sh
    cd iac/provider-aws/nomad-cluster-disk-image
    make init   # install Packer plugins
    make build  # build the AMI (~5 min, launches a t3.large)
    ```
7. Run `make build-and-upload` to build and push container images and binaries
8. Run `make copy-public-builds` to copy kernels, Firecracker versions, and busybox to your S3 buckets
9. Run `make plan-without-jobs` and then `make apply` to provision the cluster infrastructure
10. Run `make plan` and then `make apply` to deploy all Nomad jobs (this also runs database migrations automatically via the API's db-migrator task)
11. Setup data in the cluster by running `make prep-cluster` to create an initial user, team, and build a base template

### AWS Architecture

The AWS deployment provisions the following:

**Node Pools (EC2 Auto Scaling Groups):**
- **Control Server** - Nomad/Consul servers (default: 3x `t3.medium`)
- **API** - API server, ingress, client proxy, otel, loki, logs collector (default: `t3.xlarge`)
- **Client** - Firecracker orchestrator nodes with nested virtualization (default: `m8i.4xlarge`)
- **Build** - Template manager for building sandbox templates (default: `m8i.2xlarge`)
- **ClickHouse** - Analytics database (default: `t3.xlarge`)

**Managed Services (optional):**
- ElastiCache Redis (set `REDIS_MANAGED=true`)

### AWS Troubleshooting

**Bare metal instances not available**

Firecracker requires bare metal or nested virtualization support. Make sure your region supports the instance types you've selected (e.g. `m8i.4xlarge` with nested virtualization). You may need to request a service quota increase for the instance type.

**ECR authentication errors**

Run `make provider-login` to refresh your ECR authentication token. Tokens expire after 12 hours.

---

## Common

### Interacting with the cluster

#### SDK
When using SDK pass domain when creating new `Sandbox` in JS/TS SDK
```js
import { Sandbox } from "e2b";

const sandbox = await Sandbox.create({
  domain: "<your-domain>",
});
```

or in Python SDK

```python
from e2b import Sandbox

sandbox = Sandbox.create(domain="<your-domain>")
```

#### CLI
When using CLI you can pass domain as well
```sh
E2B_DOMAIN=<your-domain> e2b <command>
```

#### Monitoring and logging jobs

To access the nomad web UI, go to https://nomad.<your-domain.com>. Go to sign in, and when prompted for an API token, you can find this in your cloud provider's secrets manager (GCP Secrets Manager or AWS Secrets Manager). From here, you can see nomad jobs and tasks for both client and server, including logging.

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
- `make build-and-upload` - builds and uploads the docker images, binaries, and cluster disk image
- `make copy-public-builds` - copies busybox, kernels, and firecracker versions from the public bucket to your bucket
- `make migrate` - runs the migrations for your database
- `make provider-login` - logs in to cloud provider
- `make switch-env ENV={prod,staging,dev}` - switches the environment
- `make import TARGET={resource} ID={resource_id}` - imports the already created resources into the terraform state
- `make setup-ssh` - sets up the ssh key for the environment (useful for remote-debugging)
- `make connect-orchestrator` - establish the ssh connection to the remote orchestrator (for testing API locally)
- `make prep-cluster` - creates an initial user, team, seeds the database, and builds a base template
- `make seed-db` - seeds the database with users and teams
