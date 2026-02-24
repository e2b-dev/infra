ENV := $(shell cat ../../.last_used_env || echo "not-set")
-include ../../.env.${ENV}

AWS_BUCKET_PREFIX ?= $(PREFIX)$(AWS_ACCOUNT_ID)-
GCP_BUCKET_PREFIX ?= $(GCP_PROJECT_ID)-

HOSTNAME := $(shell hostname || hostnamectl hostname)

# Architecture for builds. Defaults to local arch; override for cross-compilation
# (e.g., BUILD_ARCH=amd64 make build from an ARM64 host).
BUILD_ARCH ?= $(shell go env GOARCH)

.PHONY: init
init:
	brew install protobuf

.PHONY: generate
generate:
	go generate ./...

.PHONY: build
build:
	$(eval COMMIT_SHA := $(shell git rev-parse --short HEAD))
	@docker build --platform linux/$(BUILD_ARCH) --output=bin --build-arg COMMIT_SHA="$(COMMIT_SHA)" -f ./Dockerfile ..

.PHONY: build-local
build-local: fetch-busybox
	# Allow for passing commit sha directly for docker builds
	$(eval COMMIT_SHA ?= $(shell git rev-parse --short HEAD))
	CGO_ENABLED=1 GOOS=linux GOARCH=$(BUILD_ARCH) go build -o bin/orchestrator -ldflags "-X=main.commitSHA=$(COMMIT_SHA)" .
	CGO_ENABLED=1 GOOS=linux GOARCH=$(BUILD_ARCH) go build -o bin/clean-nfs-cache -ldflags "-X=main.commitSHA=$(COMMIT_SHA)" ./cmd/clean-nfs-cache

.PHONY: build-debug
build-debug:
	CGO_ENABLED=1 GOOS=linux GOARCH=$(BUILD_ARCH) go build -race -gcflags=all="-N -l" -o bin/orchestrator .

.PHONY: run-debug
run-debug:
	make build-debug
	sudo -E GOTRACEBACK=crash \
	GODEBUG=madvdontneed=1 \
	NODE_ID="testclient" \
	TEMPLATE_BUCKET_NAME=$(TEMPLATE_BUCKET_NAME) \
	ENVD_TIMEOUT=$(ENVD_TIMEOUT) \
	ORCHESTRATOR_SERVICES=$(ORCHESTRATOR_SERVICES) \
	GCP_DOCKER_REPOSITORY_NAME=$(GCP_DOCKER_REPOSITORY_NAME) \
	GOOGLE_SERVICE_ACCOUNT_BASE64=$(GOOGLE_SERVICE_ACCOUNT_BASE64) \
	OTEL_COLLECTOR_GRPC_ENDPOINT=$(OTEL_COLLECTOR_GRPC_ENDPOINT) \
	MAX_PARALLEL_MEMFILE_SNAPSHOTTING=$(MAX_PARALLEL_MEMFILE_SNAPSHOTTING) \
	./bin/orchestrator

define setup_local_env
	$(eval include .env.local)
	$(eval export $(shell sed 's/=.*//' .env.local))
endef

.PHONY: run-local
run-local:
	$(call setup_local_env)
	NODE_ID=$(HOSTNAME) ./bin/orchestrator

.PHONY: upload/clean-nfs-cache
upload/clean-nfs-cache:
	chmod +x ./bin/clean-nfs-cache
ifeq ($(PROVIDER),aws)
	aws s3 cp ./bin/clean-nfs-cache "s3://${AWS_BUCKET_PREFIX}fc-env-pipeline/clean-nfs-cache" --profile ${AWS_PROFILE} --cache-control "no-cache, max-age=0"
else
	gsutil -h "Cache-Control:no-cache, max-age=0" cp ./bin/clean-nfs-cache "gs://${GCP_BUCKET_PREFIX}fc-env-pipeline/clean-nfs-cache"
endif

.PHONY: upload/orchestrator
upload/orchestrator:
	chmod +x ./bin/orchestrator
ifeq ($(PROVIDER),aws)
	aws s3 cp ./bin/orchestrator "s3://${AWS_BUCKET_PREFIX}fc-env-pipeline/orchestrator" --profile ${AWS_PROFILE} --cache-control "no-cache, max-age=0"
else
	gsutil -h "Cache-Control:no-cache, max-age=0" cp ./bin/orchestrator "gs://${GCP_BUCKET_PREFIX}fc-env-pipeline/orchestrator"
endif

.PHONY: upload/template-manager
upload/template-manager:
	chmod +x ./bin/orchestrator
ifeq ($(PROVIDER),aws)
	aws s3 cp ./bin/orchestrator "s3://${AWS_BUCKET_PREFIX}fc-env-pipeline/template-manager" --profile ${AWS_PROFILE} --cache-control "no-cache, max-age=0"
else
	gsutil -h "Cache-Control:no-cache, max-age=0" cp ./bin/orchestrator "gs://${GCP_BUCKET_PREFIX}fc-env-pipeline/template-manager"
endif

.PHONY: build-and-upload/clean-nfs-cache
build-and-upload/clean-nfs-cache: build upload/clean-nfs-cache

.PHONY: build-and-upload/orchestrator
build-and-upload/orchestrator: build upload/orchestrator

.PHONY: build-and-upload/template-manager
build-and-upload/template-manager: build upload/template-manager


.PHONY: test
test:
	go test -race -v ./...

.PHONY: test-docker
test-docker:
	@echo "Running orchestrator tests in Docker (AMD64 Linux)..."
	@rm -rf .shared/
	@cp -r ../shared .shared/
	@rm -rf .clickhouse/
	@cp -r ../clickhouse .clickhouse/
	@docker build --platform linux/$(BUILD_ARCH) -f test.Dockerfile --no-cache-filter runner --progress=plain -t orchestrator-test .
	@rm -rf .shared/
	@rm -rf .clickhouse/
	@echo "Done"

.PHONY: build-template
build-template:
	TEMPLATE_BUCKET_NAME=$(TEMPLATE_BUCKET_NAME) \
	GOOGLE_SERVICE_ACCOUNT_BASE64=$(GOOGLE_SERVICE_ACCOUNT_BASE64) \
	DOCKER_AUTH_BASE64=$(DOCKER_AUTH_BASE64) \
	GCP_PROJECT_ID=$(GCP_PROJECT_ID) \
	GCP_DOCKER_REPOSITORY_NAME=$(GCP_DOCKER_REPOSITORY_NAME) \
	GCP_REGION=$(GCP_REGION) \
	ENVIRONMENT=local \
	go run cmd/create-build/main.go \
	-template $(TEMPLATE_ID) \
	-to-build $(BUILD_ID) \
	-kernel $(KERNEL_VERSION) \
	-firecracker $(FIRECRACKER_VERSION)

.PHONY: fetch-busybox
fetch-busybox:
	@ARCH=$(BUILD_ARCH); \
	if [ "$$ARCH" = "arm64" ]; then \
		echo "Fetching arm64 busybox via apt..."; \
		TMPDIR=$$(mktemp -d); \
		apt-get download busybox-static 2>/dev/null && \
			dpkg-deb -x busybox-static_*.deb "$$TMPDIR" && \
			cp "$$TMPDIR/bin/busybox" ./internal/template/build/core/systeminit/busybox_1.36.1-2 && \
			rm -rf "$$TMPDIR" busybox-static_*.deb && \
			echo "✓ Replaced embedded busybox with arm64 binary (from busybox-static package)" || \
			{ rm -rf "$$TMPDIR" busybox-static_*.deb; echo "⚠ Failed to fetch busybox-static — install it: apt install busybox-static"; exit 1; }; \
	else \
		echo "✓ Using bundled amd64 busybox"; \
	fi

.PHONY: migrate
migrate:
	./scripts/upload-envs.sh /mnt/disks/fc-envs/v1 $(TEMPLATE_BUCKET_NAME)

.PHONY: lint
lint:
	golangci-lint run --fix ./...
