![E2B Infra Preview Light](/readme-assets/infra-light.png#gh-light-mode-only)
![E2B Infra Preview Dark](/readme-assets/infra-dark.png#gh-dark-mode-only)

# E2B Infrastructure

[E2B](https://e2b.dev) is an open-source infrastructure for AI code interpreting. In our main repository [e2b-dev/e2b](https://github.com/e2b-dev/E2B) we are giving you SDKs and CLI to customize and manage environments and run your AI agents in the cloud.

This repository contains the infrastructure that powers the E2B platform.

## Contributing

See [CONTRIBUTING.md](./CONTRIBUTING.md) for ways you can contribute to E2B Infrastructure.

## Self-hosting

Read the [self-hosting guide](./self-host.md) to learn how to set up the infrastructure on your own. The infrastructure is deployed using Terraform.

Supported cloud providers:
- 🟢 GCP
- 🟢 AWS (Beta)
- [ ] Azure
- [ ] General linux machine

## Limitations

- Custom template builds require Debian/Ubuntu-based base images (images that provide the apt package manager). Non-Debian images such as Alpine, CentOS/RHEL, or other distributions without apt are not supported and will fail during the template build/provisioning process. See [docs/limitations.md](docs/limitations.md) for details and suggested PR wording.

