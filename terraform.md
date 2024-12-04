# Terraform deployment

### Prerequisites

You will need the following installed:

- [packer](https://developer.hashicorp.com/packer/tutorials/docker-get-started/get-started-install-cli)
- [terraform](https://developer.hashicorp.com/terraform/tutorials/aws-get-started/install-cli) (v1.5 > version < 1.6)
- [atlas](https://atlasgo.io/docs#installation)
- [golang](https://go.dev/doc/install)
- [gcloud cli](https://cloud.google.com/sdk/docs/install)
- [docker](https://docs.docker.com/engine/install/)

You will also need:

- a Cloudflare account
- a domain on Cloudflare
- GCP account + project
- PostgreSQL database--Supabase preferred
  Optional but recommended for monitoring and logging:
- Grafana Account & Stack (see Step 15 for detailed notes)
- Posthog Account


Check if you can use config for terraform state management

1. [Create bucket in Google Cloud](https://cloud.google.com/storage/docs/creating-buckets) (this is the source of truth for the terraform state)
2. Create `.env.{prod,staging,dev}` from `.env.template` in the root of the repo and fill in the values. All are required except #Tests
3. Run `make switch-env ENV={prod,staging,dev}`
4. Manually create a DB in your postgres (new project in Supabase) instance and run `make migrate` (This step will fail--that's okay. After you get the error message, you will need to create `atlas_schema_revisions.atlas_schema_revisions`, just copied from `public.atlas_schema_revisions`) This can be done with the following statement in the Supabase visual SQL Editor:

```sql
CREATE TABLE  atlas_schema_revisions.atlas_schema_revisions (LIKE public.atlas_schema_revisions INCLUDING ALL);
```

5. Run `make migrate` again
6. Run `make init` (If this errors, run it a second time--it's due to a race condition on Terraform enabling API access for the various GCP services; this can take several seconds) A full list of services that will be enabled for API access:
   - [Secret Manager API](https://console.cloud.google.com/apis/library/secretmanager.googleapis.com)
   - [Certificate Manager API](https://console.cloud.google.com/apis/library/certificatemanager.googleapis.com)
   - [Compute Engine API](https://console.cloud.google.com/apis/library/compute.googleapis.com)
   - [Artifact Registry API](https://console.cloud.google.com/apis/library/artifactregistry.googleapis.com)
   - [OS Config API](https://console.cloud.google.com/apis/library/osconfig.googleapis.com)
   - [Stackdriver Monitoring API](https://console.cloud.google.com/apis/library/monitoring.googleapis.com)
   - [Stackdriver Logging API](https://console.cloud.google.com/apis/library/logging.googleapis.com)
7. Run `make build-and-upload`
8. Run `make copy-public-builds` (you can build your own kernel and firecracker version from source by running, more info bellow)
9. Secrets are created and stored in GCP Secrets Manager. Once created, that is the source of truth--you will need to update values there to make changes. Create a secret value for the following secrets:

- e2b-cloudflare-api-token
- e2b-postgres-connection-string
- Grafana secrets (optional)
- Posthog API keys for monitoring (optional)

10. Run `make plan-without-jobs` and then `make apply`
11. Run `make plan` and then `make apply`. Note: provisioning of the TLS certificates can take some time; you can check the status in the Google Cloud Console
12. To access the nomad web UI, go to nomad.<your-domain.com>. Go to sign in, and when prompted for an API token, you can find this in GCP Secrets Manager. From here, you can see nomad jobs and tasks for both client and server, including logging.
13. Look inside packages/nomad for config files for your logging and monitoring agents. Follow the steps described on Step 13 to apply changes to the agents.
15. If any problems arise, open [a Github Issue on the repo](https://github.com/e2b-dev/infra/issues) and we'll look into it.

---

### Building Firecracker and UFFD from source

You can build your own kernel and firecracker version from source by running `make build-and-upload-fc-components`

- Note: This needs to be done on a Linux machine due to case-sensitive requirements for the file system--you'll error out during the automated git section with a complaint about unsaved changes. Kernel and versions could alternatively be sourced elsewhere.
- You will still have to copy `envd-v0.0.1` from public bucket by running the command bellow or you can build it from [this commit](https://github.com/e2b-dev/infra/tree/703da3b2b8ef4af450f9874228e7406bdfc75d4a)

```
gsutil cp -r gs://e2b-prod-public-builds/envd-v0.0.1 gs://$(GCP_PROJECT_ID)-fc-env-pipeline/envd-v0.0.1
```

### Make commands cheat sheet

- `make init` - setup the terraform environment
- `make plan` - plans the terraform changes
- `make apply` - applies the terraform changes
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
