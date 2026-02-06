ENV := $(shell cat ../../.last_used_env || echo "not-set")
-include ../../.env.${ENV}

.PHONE: generate
generate:
	go generate ./...

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

.PHONY: prep-cluster
prep-cluster:
	$(MAKE) -C ../db seed-db
	$(MAKE) build-base-template

.PHONY: test
test:
	go test -v ./pkg/...
