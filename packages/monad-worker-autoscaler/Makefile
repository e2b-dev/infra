ENV := $(shell cat ../../.last_used_env || echo "not-set")
-include ../../.env.${ENV}

GCP_BUCKET_PREFIX ?= $(GCP_PROJECT_ID)-
AWS_BUCKET_PREFIX ?= $(PREFIX)$(AWS_ACCOUNT_ID)-
BINARY_NAME := monad-worker-autoscaler
BIN_DIR := bin
BINARY := $(BIN_DIR)/$(BINARY_NAME)

.PHONY: build
build:
	@mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o $(BINARY) .

.PHONY: clean
clean:
	rm -rf $(BIN_DIR)

.PHONY: test
test:
	go test -race ./...

.PHONY: upload
upload:
	$(eval COMMIT_SHA := $(shell git rev-parse --short=12 HEAD))
	@test -z "$$(git status --porcelain --untracked-files=normal)" || (printf 'Refusing to publish monad-worker-autoscaler from a dirty worktree. Commit the exact source first.\n'; exit 1)
	chmod +x $(BINARY)
ifeq ($(PROVIDER),aws)
	aws s3api put-object --bucket "${AWS_BUCKET_PREFIX}fc-env-pipeline" --key "$(BINARY_NAME).$(COMMIT_SHA)" --body "fileb://$(CURDIR)/$(BINARY)" --profile ${AWS_PROFILE} --cache-control "no-cache, max-age=0" --if-none-match '*'
else
	gcloud storage cp --if-generation-match=0 --cache-control="no-cache, max-age=0" $(BINARY) "gs://${GCP_BUCKET_PREFIX}fc-env-pipeline/$(BINARY_NAME).$(COMMIT_SHA)"
endif

.PHONY: build-and-upload
build-and-upload: build upload
