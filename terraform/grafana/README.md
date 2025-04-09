# Managed Grafana in Grafana Cloud 

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
