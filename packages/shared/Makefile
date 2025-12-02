ENV := $(shell cat ../../.last_used_env || echo "not-set")
-include ../../.env.${ENV}

.PHONY: generate-fc
generate-fc:
	cd pkg/fc && go tool swagger generate client -f firecracker.yml -A firecracker

.PHONE: generate
generate: generate-fc

.PHONY: build-base-template
build-base-template:
	@echo "Building base template..."
	@cd scripts; \
    	npm install --silent; \
        E2B_API_KEY=$(E2B_API_KEY); \
        if [ -z "$$E2B_API_KEY" ]; then \
			read -p "Enter E2B_API_KEY for the base template team: " E2B_API_KEY; \
		fi; \
		E2B_API_KEY=$$E2B_API_KEY E2B_DOMAIN=$(DOMAIN_NAME) \
    	npx tsx build.prod.ts
	@echo "Done"

.PHONE: seed-db
seed-db:
	@echo "Seeding database..."
	@POSTGRES_CONNECTION_STRING=$(POSTGRES_CONNECTION_STRING) go run ./scripts/seed/post

.PHONY: prep-cluster
prep-cluster:
	$(MAKE) seed-db
	$(MAKE) build-base-template

.PHONY: test
test:
	go test -v ./pkg/...
