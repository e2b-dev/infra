# E2B Infrastructure 

[E2B](https://e2b.dev) is an open-source infrastructure for AI code interpreting. In our main repository [e2b-dev/e2b](https://github.com/e2b-dev/E2B) we are giving you SDKs and CLI to customize and manage environments and run your AI agents in the cloud.

This repository contains the infrastructure that powers the E2B platform.

## Self-hosting

The infrastructure is deployed using [Terraform](./terraform.md) and right now it is deployable on GCP only.

Setting the infrastructure up can be a little rough right now, but we plan to improve it in the future.

## Project Structure

In this monorepo, there are several components written in Go and a Terraform configuration for the deployment.

The main components are:

1. [API server](./packages/api/)
1. [Daemon running inside instances (sandboxes)](./packages/envd/)
1. [Service for managing instances (sandboxes)](./packages/orchestrator/)
1. [Service for building environments (templates)](./packages/template-manager/)

The following diagram shows the architecture of the whole project:
![E2B infrastructure diagram](./readme-assets/architecture.jpeg)
