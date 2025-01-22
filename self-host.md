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
2. Create `.env.prod`, `.env.staging`, or `.env.dev` from [`.env.template`](.env.template). You can pick any of them. Make sure to fill in the values. All are required.
3. Run `make switch-env ENV={prod,staging,dev}` to start using your env
4. Run `make login-gcloud` to login to `gcloud`
5. [Create a storage bucket in Google Cloud](https://cloud.google.com/storage/docs/creating-buckets). This is the source of truth for the terraform state: Go to `console.cloud.google.com` -> Storage -> Create Bucket -> Bucket name: `e2b-terraform-state` -> Location: `US` -> Default storage class: `Standard` -> Location type: `Multi-region` -> Bucket location: `US` -> Create
6. Run `make init`. If this errors, run it a second time--it's due to a race condition on Terraform enabling API access for the various GCP services; this can take several seconds. A full list of services that will be enabled for API access:
   - [Secret Manager API](https://console.cloud.google.com/apis/library/secretmanager.googleapis.com)
   - [Certificate Manager API](https://console.cloud.google.com/apis/library/certificatemanager.googleapis.com)
   - [Compute Engine API](https://console.cloud.google.com/apis/library/compute.googleapis.com)
   - [Artifact Registry API](https://console.cloud.google.com/apis/library/artifactregistry.googleapis.com)
   - [OS Config API](https://console.cloud.google.com/apis/library/osconfig.googleapis.com)
   - [Stackdriver Monitoring API](https://console.cloud.google.com/apis/library/monitoring.googleapis.com)
   - [Stackdriver Logging API](https://console.cloud.google.com/apis/library/logging.googleapis.com)
7. Run `make build-and-upload`
8. Run `make copy-public-builds`. This will copy kernel and rootfs builds for Firecracker to your bucket. You can [build your own](#building-firecracker-and-uffd-from-source) kernel and Firecracker roots.
9. Get Cloudflare API Token: go to the [Cloudflare dashboard](https://dash.cloudflare.com/) -> Manage Account -> API Tokens -> Create Token -> Edit Zone DNS -> in "Zone Resources" select your domain and generate the token
10. Get Postgres database connection string from your database 
    - e.g. [from Supabase](https://supabase.com/docs/guides/database/connecting-to-postgres#direct-connection): Create a new project in Supabase and go to your project in Supabase -> Settings -> Database -> Connection Strings -> Postgres -> Direct 
11. Run `make migrate`
12. Secrets are created and stored in GCP Secrets Manager. Once created, that is the source of truth--you will need to update values there to make changes. Create a secret value for the following secrets:
- e2b-cloudflare-api-token
- e2b-postgres-connection-string
- Grafana secrets (optional)
- Posthog API keys for monitoring (optional)
13. Run `make plan-without-jobs` and then `make apply`
14. Run `make plan` and then `make apply`. Note: This will work after the TLS certificates was issued. It1 can take some time; you can check the status in the Google Cloud Console
15. Either run 
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

#### Dashboard

You can also set your domain in the dashboard at [Developer settings](https://e2b.dev/dashboard?tab=developer)


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
- You will still have to copy `envd-v0.0.1` from public bucket by running the command bellow or you can build it from [this commit](https://github.com/e2b-dev/infra/tree/703da3b2b8ef4af450f9874228e7406bdfc75d4a)

```
gsutil cp -r gs://e2b-prod-public-builds/envd-v0.0.1 gs://$(GCP_PROJECT_ID)-fc-env-pipeline/envd-v0.0.1
```

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
- `make login-gcloud` - logs in to gcloud
- `make switch-env ENV={prod,staging,dev}` - switches the environment
- `make import TARGET={resource} ID={resource_id}` - imports the already created resources into the terraform state
- `make setup-ssh` - sets up the ssh key for the environment (useful for remote-debugging)
