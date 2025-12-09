ENV := $(shell cat ../../.last_used_env || echo "not-set")
-include ../../.env.${ENV}

BUCKET_PREFIX ?= $(PREFIX)$(AWS_ACCOUNT_ID)-

.PHONY: build
build:
	./scripts/build.sh

.PHONY: upload
upload:
ifeq ($(PROVIDER),aws)
	AWS_BUCKET_PREFIX ?= $(PREFIX)$(AWS_ACCOUNT_ID)-
	aws s3 cp builds/ "s3://${AWS_BUCKET_PREFIX}fc-versions/" -recursive --cache-control "no-cache, max-age=0"
else
	gsutil -h "Cache-Control:no-cache, max-age=0" cp -r "builds/*" "gs://${GCP_PROJECT_ID}-fc-versions"
	if [ "$GCP_PROJECT_ID" == "e2b-prod" ]; then
	  # Upload kernel to GCP public builds bucket
	  gsutil -h "Cache-Control:no-cache, max-age=0" cp -r "builds/*" "gs://${GCP_PROJECT_ID}-public-builds/firecrackers/"
	fi
endif
	rm -rf builds/*

.PHONY: build-and-upload
make build-and-upload: build upload
