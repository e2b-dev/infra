# Deploy Redis

## Prequisites

- Import the default subnetwork by running `make import TARGET=module.redis[0].google_compute_subnetwork.default ID=default` in the root `/` directory.

## Deploy

- run `make plan` to plan the changes
- run `make apply` to apply the changes 
