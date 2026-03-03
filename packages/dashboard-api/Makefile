ENV := $(shell cat ../../.last_used_env || echo "not-set")
-include ../../.env.${ENV}
PREFIX := $(strip $(subst ",,$(PREFIX)))

expectedMigration := $(shell ./../../scripts/get-latest-migration.sh)

ifeq ($(PROVIDER),aws)
	IMAGE_REGISTRY := $(AWS_ACCOUNT_ID).dkr.ecr.$(AWS_REGION).amazonaws.com/$(PREFIX)core/dashboard-api
else
	IMAGE_REGISTRY := $(GCP_REGION)-docker.pkg.dev/$(GCP_PROJECT_ID)/$(PREFIX)core/dashboard-api
endif

HOSTNAME := $(shell hostname 2> /dev/null || hostnamectl hostname 2> /dev/null)
$(if $(HOSTNAME),,$(error Failed to determine hostname: both 'hostname' and 'hostnamectl' failed))

.PHONY: generate
generate:
	go generate ./...

.PHONY: build
build:
	# Allow for passing commit sha directly for docker builds
	$(eval COMMIT_SHA ?= $(shell git rev-parse --short HEAD))
	$(eval EXPECTED_MIGRATION_TIMESTAMP ?= $(expectedMigration))
	CGO_ENABLED=0 go build -o bin/dashboard-api -ldflags "-X=main.commitSHA=$(COMMIT_SHA) -X=main.expectedMigrationTimestamp=$(EXPECTED_MIGRATION_TIMESTAMP)" .

.PHONY: build-and-upload
build-and-upload:
		$(eval COMMIT_SHA := $(shell git rev-parse --short HEAD))
		$(eval EXPECTED_MIGRATION_TIMESTAMP := $(expectedMigration))
		@ docker build --platform linux/amd64 --tag $(IMAGE_REGISTRY) --push --build-arg COMMIT_SHA="$(COMMIT_SHA)" --build-arg EXPECTED_MIGRATION_TIMESTAMP="$(EXPECTED_MIGRATION_TIMESTAMP)"  -f ./Dockerfile ..

.PHONY: run
run:
	make build
	./bin/dashboard-api

.PHONY: run-local
run-local:
	make build
	NODE_ID=$(HOSTNAME) ./bin/dashboard-api

.PHONY: test
test:
	go test -race -v ./...
