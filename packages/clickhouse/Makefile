ENV := $(shell cat ../../.last_used_env || echo "not-set")
-include ../../.env.${ENV}

IMAGE := e2b-orchestration/clickhouse-migrator

.PHONY: build-and-upload
build-and-upload:
	@docker build --platform linux/amd64 --tag "$(GCP_REGION)-docker.pkg.dev/$(GCP_PROJECT_ID)/$(IMAGE)" --push .


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
