# Deploy grafana 

## Prerequisites

- complete the steps in [self-host.md](../../self-host.md)
- create an access policy for the grafana cloud with following permissions
Realms:
 - `<org name>` (all stacks)
Scopes:
 - accesspolicies:read
 - accesspolicies:write
 - accesspolicies:delete
 - stacks:read
 - stacks:write
 - stacks:delete
 - orgs:read
 - stack-service-accounts:write

- fill it in gcloud secrets manager (grafana-api-key)

## Deploy

- run `make plan` to plan the changes
- run `make apply` to apply the changes 
