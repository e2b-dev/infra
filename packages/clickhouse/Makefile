ENV := $(shell cat ../../.last_used_env || echo "not-set")
-include ../../.env.${ENV}

CLICKHOUSE_MIGRATOR_IMAGE ?= $(GCP_REGION)-docker.pkg.dev/$(GCP_PROJECT_ID)/e2b-orchestration/clickhouse-migrator
CLICKHOUSE_HOST ?= clickhouse

.PHONY: migrate
migrate:build
	@echo "Applying Clickhouse migrations *$(notdir $@)*"
	@docker network create e2b || true
	@docker run --rm \
		--network e2b \
		--name clickhouse-migrator \
		-e GOOSE_DBSTRING="clickhouse://${CLICKHOUSE_USERNAME}:${CLICKHOUSE_PASSWORD}@${CLICKHOUSE_HOST}:${CLICKHOUSE_PORT}/${CLICKHOUSE_DATABASE}" \
		-v "$(shell pwd)/migrations:/app/migrations" \
		$(CLICKHOUSE_MIGRATOR_IMAGE)
	@echo "Done"

.PHONY: build
build:
	@echo "$(CLICKHOUSE_MIGRATOR_IMAGE)"
	@docker build --platform linux/amd64 --tag "$(CLICKHOUSE_MIGRATOR_IMAGE)" .

.PHONY: build-and-upload
build-and-upload:build
	@docker push "$(CLICKHOUSE_MIGRATOR_IMAGE)"
	
.PHONY: build-locally
build-locally:
	docker build --no-cache --tag "$(IMAGE)" .

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
