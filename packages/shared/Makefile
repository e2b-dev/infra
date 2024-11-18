ENV := $(shell cat ../../.last_used_env || echo "not-set")
-include ../../.env.${ENV}

.PHONY: generate-fc
generate-fc:
	cd pkg/fc && swagger generate client -f firecracker.yml -A firecracker

.PHONY: generate-models
generate-models:
	rm -rf pkg/models/*
	go generate ./pkg/generate_models.go

check-atlas:
	@if ! command -v atlas >/dev/null 2>&1; then \
		echo "Atlas is not installed. Do you want to install it?  (Y/n): "; \
		read choice; \
		if [ "$$choice" = "y" ]; then \
			curl -sSf https://atlasgo.sh | ATLAS_VERSION=v0.25.0 sh; \
		else \
			exit 1; \
		fi; \
	fi

.PHONY: diff-migrations
diff-migrations: check-atlas
	atlas migrate diff --env local

.PHONY: migrate
migrate: check-atlas
	atlas migrate apply --env local --url $(POSTGRES_CONNECTION_STRING) --baseline "20000101000000"
