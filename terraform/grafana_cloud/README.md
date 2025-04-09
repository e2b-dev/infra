# Deploy grafana 

It has to be separate terraform if we want to keep it optionally, it needs to initialize the grafana provider inside the module, but that isn't compatible with conditional module invocation (e.g. count = 0).

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
 - stack-plugins:write
 - stack-plugins:delete
 - stack-plugins:read

- fill it in gcloud secrets manager (grafana-api-key)


## Deploy

- run `make plan` to plan the changes
- run `make apply` to apply the changes 
