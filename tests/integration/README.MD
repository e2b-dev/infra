# Integration Tests
Package for defining integration tests. Currently, there is a setup for API and Orchestrator testing.

## Run locally
1) Setup env variables in the .env file
2) If necessary, run `make connect-orchestrator` to create a tunnel to one orchestrator client VM in GCP
3) Run `make test`