# Self-hosting E2B

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

- [Atlas](https://atlasgo.io/docs#installation)
  - Used for database migrations
  - We don't use Atlas's hosted service, only their [open-source CLI tool](https://atlasgo.io/cli-reference) which unfortunatelly requires you to login via `atlas login`.
  - We're in the process of removing this dependency.

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
2. Run `make login-gcloud` to login to `gcloud`
3. Get Cloudflare API Token: go to the [Cloudflare dashboard](https://dash.cloudflare.com/) -> Manage Account -> API Tokens -> Create Token -> Edit Zone DNS -> in "Zone Resources" select your domain and generate the token
4. Get [Postgres database connection string from Supabase](https://supabase.com/docs/guides/database/connecting-to-postgres#direct-connection): Create a new project in Supabase and go to your project in Supabase -> Settings -> Database -> Connection Strings -> Postgres -> Direct
5. [Create a storage bucket in Google Cloud](https://cloud.google.com/storage/docs/creating-buckets). This is the source of truth for the terraform state: Go to `console.cloud.google.com` -> Storage -> Create Bucket -> Bucket name: `e2b-terraform-state` -> Location: `US` -> Default storage class: `Standard` -> Location type: `Multi-region` -> Bucket location: `US` -> Create
6. Create `.env.prod`, `.env.staging`, or `.env.dev` from [`.env.template`](.env.template). You can pick any of them. Make sure to fill in the values. All are required except the `# Tests` section
7. Run `make switch-env ENV={prod,staging,dev}`
8. Login to the Atlas CLI: `atlas login`
9. Run `make migrate`: This step will fail--that's okay. After you get the error message, you will need to create `atlas_schema_revisions.atlas_schema_revisions`, just copied from `public.atlas_schema_revisions`. This can be done with the following statement in the Supabase visual SQL Editor:

```sql
CREATE TABLE atlas_schema_revisions.atlas_schema_revisions (LIKE public.atlas_schema_revisions INCLUDING ALL);
```

10. Run `make migrate` again
11. Run `make init`. If this errors, run it a second time--it's due to a race condition on Terraform enabling API access for the various GCP services; this can take several seconds. A full list of services that will be enabled for API access:
   - [Secret Manager API](https://console.cloud.google.com/apis/library/secretmanager.googleapis.com)
   - [Certificate Manager API](https://console.cloud.google.com/apis/library/certificatemanager.googleapis.com)
   - [Compute Engine API](https://console.cloud.google.com/apis/library/compute.googleapis.com)
   - [Artifact Registry API](https://console.cloud.google.com/apis/library/artifactregistry.googleapis.com)
   - [OS Config API](https://console.cloud.google.com/apis/library/osconfig.googleapis.com)
   - [Stackdriver Monitoring API](https://console.cloud.google.com/apis/library/monitoring.googleapis.com)
   - [Stackdriver Logging API](https://console.cloud.google.com/apis/library/logging.googleapis.com)
12. Run `make build-and-upload`
13. Run `make copy-public-builds`. This will copy kernel and rootfs builds for Firecracker to your bucket. You can [build your own](#building-firecracker-and-uffd-from-source) kernel and Firecracker roots.
14. Secrets are created and stored in GCP Secrets Manager. Once created, that is the source of truth--you will need to update values there to make changes. Create a secret value for the following secrets:

- e2b-cloudflare-api-token
- e2b-postgres-connection-string
- Grafana secrets (optional)
- Posthog API keys for monitoring (optional)

15. Run `make plan-without-jobs` and then `make apply`
16. Run `make plan` and then `make apply`. Note: provisioning of the TLS certificates can take some time; you can check the status in the Google Cloud Console
17. To access the nomad web UI, go to nomad.<your-domain.com>. Go to sign in, and when prompted for an API token, you can find this in GCP Secrets Manager. From here, you can see nomad jobs and tasks for both client and server, including logging.
18. Look inside packages/nomad for config files for your logging and monitoring agents.
19. If any problems arise, open [a Github Issue on the repo](https://github.com/e2b-dev/infra/issues) and we'll look into it.

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
- `make destroy` - destroys the cluster
- `make version` - increments the repo version
- `make build-and-upload` - builds and uploads the docker images, binaries, and cluster disk image
- `make copy-public-builds` - copies the old envd binary, kernels, and firecracker versions from the public bucket to your bucket
- `make migrate` - runs the migrations for your database
- `make update-api` - updates the API docker image
- `make switch-env ENV={prod,staging,dev}` - switches the environment
- `make import TARGET={resource} ID={resource_id}` - imports the already created resources into the terraform state
- `make setup-ssh` - sets up the ssh key for the environment (useful for remote-debugging)
