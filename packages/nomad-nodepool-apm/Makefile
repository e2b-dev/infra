ENV := $(shell cat ../../.last_used_env || echo "not-set")
-include ../../.env.${ENV}

AWS_BUCKET_PREFIX ?= $(PREFIX)$(AWS_ACCOUNT_ID)-

PLUGIN_NAME := nomad-nodepool-apm
BIN_DIR := bin
BINARY := $(BIN_DIR)/$(PLUGIN_NAME)

.PHONY: build
build:
	@mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o $(BINARY) .

.PHONY: clean
clean:
	rm -rf $(BIN_DIR)

.PHONY: test
test:
	go test -v ./...

.PHONY: upload
upload:
	chmod +x $(BINARY)
ifeq ($(PROVIDER),aws)
	aws s3 cp $(BINARY) "s3://${AWS_BUCKET_PREFIX}fc-env-pipeline/nomad-nodepool-apm" --profile ${AWS_PROFILE} --cache-control "no-cache, max-age=0"
else
	gsutil -h "Cache-Control:no-cache, max-age=0" cp $(BINARY) "gs://${GCP_PROJECT_ID}-fc-env-pipeline/nomad-nodepool-apm"
endif

.PHONY: build-and-upload
build-and-upload: build upload
