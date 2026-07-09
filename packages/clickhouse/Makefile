ENV := $(shell cat ../../.last_used_env || echo "not-set")
-include ../../.env.${ENV}
PREFIX := $(strip $(subst ",,$(PREFIX)))

CLICKHOUSE_HOST ?= clickhouse

ifeq ($(PROVIDER),aws)
	CLICKHOUSE_MIGRATOR_IMAGE ?= $(AWS_ACCOUNT_ID).dkr.ecr.$(AWS_REGION).amazonaws.com/$(PREFIX)core/clickhouse-migrator
else
	CLICKHOUSE_MIGRATOR_IMAGE ?= $(GCP_REGION)-docker.pkg.dev/$(GCP_PROJECT_ID)/$(PREFIX)core/clickhouse-migrator
endif

# E2B-hosted registry that holds released, prebuilt clickhouse-migrator images.
# Published to by the release-please workflow on tagging a release.
E2B_ARTIFACTS_REGISTRY ?= us-docker.pkg.dev/e2b-artifacts/clickhouse-migrator/clickhouse-migrator

# Optional version "hook" for the released-image flow.
# When empty (default), build-and-upload builds from source and pushes to the
# client's own core repo (the existing flow, unchanged).
# When set (e.g. CLICKHOUSE_MIGRATOR_VERSION=v0.1.0), skip building: pull the
# released image from $(E2B_ARTIFACTS_REGISTRY) and retag/push it into the
# client's core repo, so the Terraform `clickhouse-migrator:latest` lookup
# keeps working.
#
# Set it either on the command line (make ... CLICKHOUSE_MIGRATOR_VERSION=v0.1.0)
# or in the active .env.${ENV} file (CLICKHOUSE_MIGRATOR_VERSION=v0.1.0), which
# is included above. Quotes are stripped so both `=v0.1.0` and `="v0.1.0"` work;
# a command-line value always wins over the env file.
CLICKHOUSE_MIGRATOR_VERSION := $(strip $(subst ",,$(CLICKHOUSE_MIGRATOR_VERSION)))

.PHONY: migrate
migrate: build migrate-without-build

.PHONY: migrate-without-build
migrate-without-build:
	@echo "Applying Clickhouse migrations *$(notdir $@)*"
	@docker network create e2b || true
	@docker run --rm \
		--network e2b \
		--name clickhouse-migrator \
		-e GOOSE_DBSTRING="clickhouse://${CLICKHOUSE_USERNAME}:${CLICKHOUSE_PASSWORD}@${CLICKHOUSE_HOST}:${CLICKHOUSE_PORT}/${CLICKHOUSE_DATABASE}" \
		-v "$(shell pwd)/migrations:/app/migrations" \
		$(CLICKHOUSE_MIGRATOR_IMAGE)
	@echo "Done"

.PHONY: migrate-local
migrate-local:
	@echo "Applying Clickhouse migrations for local environment"
	GOOSE_DBSTRING="clickhouse://clickhouse:clickhouse@localhost:9000/default" go tool goose -table "_migrations" -dir "migrations" clickhouse up
	@echo "Done"

.PHONY: build
build:
	$(eval COMMIT_SHA := $(shell git rev-parse --short HEAD))
	@docker build --platform linux/amd64 --tag "$(CLICKHOUSE_MIGRATOR_IMAGE)" --tag "$(CLICKHOUSE_MIGRATOR_IMAGE):$(COMMIT_SHA)" .

.PHONY: build-and-upload
ifeq ($(strip $(CLICKHOUSE_MIGRATOR_VERSION)),)
# Existing flow: build from source and push to the client's own core repo.
build-and-upload:build
	$(eval COMMIT_SHA := $(shell git rev-parse --short HEAD))
	@docker push "$(CLICKHOUSE_MIGRATOR_IMAGE)"
	@docker push "$(CLICKHOUSE_MIGRATOR_IMAGE):$(COMMIT_SHA)"
else
# Released-image flow: pull the prebuilt, versioned image from the E2B
# artifacts registry and retag/push it into the client's own core repo.
build-and-upload:
	@echo "Using released clickhouse-migrator $(CLICKHOUSE_MIGRATOR_VERSION) from $(E2B_ARTIFACTS_REGISTRY)"
	docker pull --platform linux/amd64 $(E2B_ARTIFACTS_REGISTRY):$(CLICKHOUSE_MIGRATOR_VERSION)
	docker tag $(E2B_ARTIFACTS_REGISTRY):$(CLICKHOUSE_MIGRATOR_VERSION) $(CLICKHOUSE_MIGRATOR_IMAGE):latest
	docker tag $(E2B_ARTIFACTS_REGISTRY):$(CLICKHOUSE_MIGRATOR_VERSION) $(CLICKHOUSE_MIGRATOR_IMAGE):$(CLICKHOUSE_MIGRATOR_VERSION)
	docker push $(CLICKHOUSE_MIGRATOR_IMAGE):latest
	docker push $(CLICKHOUSE_MIGRATOR_IMAGE):$(CLICKHOUSE_MIGRATOR_VERSION)
endif

.PHONY: connect-clickhouse
connect-clickhouse:
	CLICKHOUSE_IG=$$(gcloud compute instance-groups list \
		--project=$(GCP_PROJECT_ID) \
		--filter="name~'^.*clickhouse.*'" \
		--format='value(name)' | head -n1) && \
	INSTANCE_INFO=$$(gcloud compute instance-groups list-instances "$$CLICKHOUSE_IG" --project=$(GCP_PROJECT_ID) --zone=$(GCP_ZONE) --format='value(instance,ZONE)' | head -n1) && \
	INSTANCE_ID=$$(echo "$$INSTANCE_INFO" | awk '{print $$1}') && \
	INSTANCE_ZONE=$$(echo "$$INSTANCE_INFO" | awk '{print $$2}') && \
	gcloud compute ssh "$$INSTANCE_ID" --project=$(GCP_PROJECT_ID) --zone=$$INSTANCE_ZONE -- -NL 9000:localhost:9000 -o PermitLocalCommand=yes -o LocalCommand="echo 'SSH tunnel established'"

.PHONY: run
run:
	# Fill in the variables in the config file
	PASSWORD="$(CLICKHOUSE_PASSWORD)" \
 	USERNAME="$(CLICKHOUSE_USERNAME)" \
 	PORT="$(CLICKHOUSE_PORT)" \
	envsubst < ./local/config.tpl.xml > "./local/config.xml"

	# Fill in the variables in the users file
	PASSWORD="$(CLICKHOUSE_PASSWORD)" \
	USERNAME="$(CLICKHOUSE_USERNAME)" \
	envsubst < ./local/users.tpl.xml > "./local/users.xml"

	chmod 644 ./local/config.xml
	chmod 644 ./local/users.xml

	docker network create e2b || true
	docker run --rm --name clickhouse \
				--network e2b \
				-p 9000:9000 \
				--health-cmd="clickhouse-client --query 'SELECT 1'" \
				--health-interval=5s \
				--health-timeout=2s \
				--health-retries=5 \
				-v ./local/users.xml:/etc/clickhouse-server/users.d/users.xml \
				-v ./local/config.xml:/etc/clickhouse-server/config.d/config.xml \
				clickhouse/clickhouse-server:25.4.5.24
