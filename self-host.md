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
3. Run `make set-env ENV={prod,staging,dev}` to start using your env
4. Run `make login-gcloud` to login to `gcloud`
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
  - e2b-postgres-connection-string
  - e2b-supabase-jwt-secrets (optional / required to self-host the [E2B dashboard](https://github.com/e2b-dev/dashboard))
      > Get Supabase JWT Secret: go to the [Supabase dashboard](https://supabase.com/dashboard) -> Select your Project -> Project Settings -> Data API -> JWT Settings
11. Run `make plan` and then `make apply`. Note: This will work after the TLS certificates was issued. It can take some time; you can check the status in the Google Cloud Console
12. Setup data in the cluster by running `make prep-cluster` in `packages/shared` to create an initial user, team, and build a base template.
  - You can also run `make seed-db` in `packages/db` to create more users and teams.


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
- `make provider-login` - logs in to cloud provider
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
