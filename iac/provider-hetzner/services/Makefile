.PHONY: help build deploy install-systemd uninstall-systemd verify

# MaxiCore orchestrator + envd Deploy Pipeline (NX.5)
#
# Pre-requisites:
#   - HCLOUD_TOKEN (for terraform-applied infra)
#   - HETZNER_OBJECT_STORAGE_ACCESS_KEY + SECRET_KEY
#   - SSH access to PRIMARY (157.90.13.250)
#
# Usage:
#   make build            — cross-compile orchestrator + envd binaries (linux/amd64)
#   make deploy           — full deploy via deploy-to-primary.sh
#   make install-systemd  — install systemd units on local PRIMARY (run via SSH)
#   make verify           — verify orchestrator :50051 + envd :49983 on PRIMARY

REPO_ROOT := $(shell git rev-parse --show-toplevel)
PRIMARY_HOST ?= 157.90.13.250
DEPLOY_VERSION ?= $(shell git rev-parse --short HEAD)

help:
	@echo "MaxiCore orchestrator + envd Deploy"
	@echo ""
	@echo "Targets:"
	@echo "  build            — cross-compile binaries"
	@echo "  deploy           — upload + atomic-swap on PRIMARY"
	@echo "  install-systemd  — install systemd units (local, via SSH)"
	@echo "  verify           — health-check on PRIMARY"

build:
	cd $(REPO_ROOT)/packages/orchestrator && \
		GOOS=linux GOARCH=amd64 CGO_ENABLED=0 \
		go build -o /tmp/orchestrator-bin -ldflags "-X main.commitSHA=$(DEPLOY_VERSION)" .
	cd $(REPO_ROOT)/packages/envd && \
		GOOS=linux GOARCH=amd64 CGO_ENABLED=0 \
		go build -o /tmp/envd-bin -ldflags "-X main.commitSHA=$(DEPLOY_VERSION)" .
	@echo "✅ Built: /tmp/orchestrator-bin + /tmp/envd-bin"

deploy:
	bash scripts/deploy-to-primary.sh

install-systemd:
	scp orchestrator/orchestrator.service root@$(PRIMARY_HOST):/etc/systemd/system/
	scp envd/envd.service root@$(PRIMARY_HOST):/etc/systemd/system/
	ssh root@$(PRIMARY_HOST) "systemctl daemon-reload && systemctl enable orchestrator envd"
	@echo "✅ systemd units installed (services NOT yet started — run 'make deploy' first)"

uninstall-systemd:
	ssh root@$(PRIMARY_HOST) "systemctl stop orchestrator envd 2>/dev/null; systemctl disable orchestrator envd 2>/dev/null; rm -f /etc/systemd/system/orchestrator.service /etc/systemd/system/envd.service; systemctl daemon-reload"

verify:
	@ssh root@$(PRIMARY_HOST) "systemctl is-active orchestrator && systemctl is-active envd"
	@ssh root@$(PRIMARY_HOST) "ss -tlnp | grep -E '(50051|49983)'"
	@echo "✅ orchestrator :50051 + envd :49983 healthy"
