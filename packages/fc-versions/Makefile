ENV := $(shell cat ../../.last_used_env || echo "not-set")
-include ../../.env.${ENV}

BUCKET_PREFIX ?= $(PREFIX)$(AWS_ACCOUNT_ID)-

.PHONY: build
build:
	./scripts/build.sh

.PHONY: upload
upload:
ifeq ($(PROVIDER),aws)
	./scripts/upload-aws.sh "$(BUCKET_PREFIX)" "$(AWS_PROFILE)"
else
	./scripts/upload-gcp.sh $(GCP_PROJECT_ID)
endif

.PHONY: build-and-upload
make build-and-upload: build upload
