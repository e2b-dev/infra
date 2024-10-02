# Terraform deployment

### Prerequisites
You will need the following installed:
- packer
- terraform (v1.5 > version < 1.6)
- atlas
- golang
- gcloud cli
- docker

You will also need:
- a Cloudflare account
- a domain on Cloudflare
- GCP account + project
- PostgreSQL database--Supabase preferred
Optional but recommended for monitoring and logging:
- Grafana Account & Stack (see Step 15 for detailed notes)
- Posthog Account

Lastly, Step 8 *require you to be on Linux* (explanation on step 8 for those interested). These are building Firecracker kernels and required versions--in the future, we will have these pre-built and available for ease-of-use.


Check if you can use config for terraform state management

1. Create bucket in Google Cloud
2. Create `.env.prod` from `.env.template` and fill in the values. All are required except #Tests
3. Run `make switch-env ENV=prod`
4. Manually create a DB in Supabase and run `make migrate` (This step will fail--that's okay. After you get the error message, you will need to create atlas_schema_revisions.atlas_schema_revisions, just copied from public.atlas_schema_revisions) This can be done with the following statement in the Supabase visual SQL Editor:
```
CREATE TABLE  atlas_schema_revisions.atlas_schema_revisions (LIKE public.atlas_schema_revisions INCLUDING ALL); 
```
5. Run `make init` (If this errors, run it a second time--it's due to a race condition on Terraform enabling API access for the various GCP services; this can take several seconds) A full list of services that will be enabled for API access:
   - [Secret Manager API](https://console.cloud.google.com/apis/library/secretmanager.googleapis.com)
   - [Certificate Manager API](https://console.cloud.google.com/apis/library/certificatemanager.googleapis.com)
   - [Compute Engine API](https://console.cloud.google.com/apis/library/compute.googleapis.com)
   - [Artifact Registry API](https://console.cloud.google.com/apis/library/artifactregistry.googleapis.com)
   - [OS Config API](https://console.cloud.google.com/apis/library/osconfig.googleapis.com)
   - [Stackdriver Monitoring API](https://console.cloud.google.com/apis/library/monitoring.googleapis.com)
   - [Stackdriver Logging API](https://console.cloud.google.com/apis/library/logging.googleapis.com)
6. Run `make build-cluster-disk-image`
7. Run `make build-and-upload-docker-images`
8. Run `make build-and-upload-fc-components` Note: This needs to be done on a Linux machine due to case-sensitive requirements for the file system--you'll error out during the automated git section with a complaint about unsaved changes. Kernel and versions could alternatively be sourced elsewhere.
9. At the time of this writing, several versions are required. The script may not fully create and upload these. As of 9/27/24, your Storage buckets should look like this:  
```
<prefix>-fc-env-pipeline/envd  
                        /envd-v.0.0.1 (this is legacy)  
                        /orchestrator  
                        /template-manager

<prefix>-fc-kernels/vmlinux-5.10.186/vmlinux.bin

<prefix>-fc-versions/v1.7.0-dev_8bb88311/firecracker
                                        /uffd
                    /v1.8.0-hugepages-state_4778a02/firecracker
                    /v1.8.0-hugepages-state_9e0b47a/firecracker
                    /v1.8.0-main_43ff620/firecracker
                    /v1.9.0_fake-2476009/firecracker
                                        /uffd

```
10. Run `make apply-without-jobs`
11. Secrets are created and stored in GCP Secrets Manager. Once created, that is the source of truth--you will need to update values there to make changes.   
Some notes:  
- You can optionally add Grafana and Posthog API keys for monitoring
- When changing env vars, currently you will need to purge the job in nomad (see below for nomad instructions), then re-run `make apply` for the new variables to be properly sourced
12. Run `make apply`. Note: provisioning of the TLS certificates can take some time; you can check the status in the Google Cloud Console
13. To access the nomad web UI, go to nomad.<your-domain.com>. Go to sign in, and when prompted for an API token, you can find this in GCP Secrets Manager. From here, you can see nomad jobs and tasks for both client and server, including logging.
14. Look inside packages/nomad for config files for your logging and monitoring agents. Follow the steps described on Step 13 to apply changes to the agents.
15. As of 9/27/24, GCP Secrets Manager does not auto-populate with Grafana or Posthog API credentials from the .env file. You will need to manually fill in these Secret values. For Grafana, these values can be found not inside the Stack, but from the `Details` button on your Grafana account when choosing a Stack, then details for each plugin.












