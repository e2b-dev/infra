# Deploy grafana 

## Prerequisites

- complete the steps in [self-host.md](../../self-host.md)
- create a access token for the otel collector with permissions: 

Realms:
 - `<org name>` (all stacks)
Scopes:
 - accesspolicies:read
 - stacks:read
 - orgs:read
 - accesspolicies:write
 - stack-service-accounts:write
 - stacks:write
 - accesspolicies:delete
 - stacks:delete

## Deploy

- run `make plan` to plan the changes
- run `make apply` to apply the changes 
