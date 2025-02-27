ENV := $(shell cat ../../.last_used_env || echo "not-set")
-include ../../.env.${ENV}

client := gcloud compute instances list --format='csv(name)' --project $(GCP_PROJECT_ID) | grep "client"

.PHONY: init
init:
	brew install protobuf
	go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.28
	go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.2

.PHONY: generate
generate:
	# You need to install protobuf (brew install protobuf) and following go packages: protoc-gen-go, protoc-gen-go-grpc
	# https://grpc.io/docs/languages/go/quickstart/
	@echo "Generating..."
	@protoc --go_out=../shared/pkg/grpc/template-manager/ --go_opt=paths=source_relative --go-grpc_out=../shared/pkg/grpc/template-manager/ --go-grpc_opt=paths=source_relative template-manager.proto
	@echo "Done"

.PHONY: build
build:
	# Allow for passing commit sha directly for docker builds
	$(eval COMMIT_SHA ?= $(shell git rev-parse --short HEAD))
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o bin/template-manager -ldflags "-X=main.commitSHA=$(COMMIT_SHA)"  .

.PHONY: build-debug
build-debug:
	CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build -race -gcflags=all="-N -l" -o bin/template-manager .

.PHONY: upload
upload:
	./upload.sh $(GCP_PROJECT_ID)

.PHONY: build-and-upload
build-and-upload: build upload

.PHONY: test
test:
	go test -v ./...

.PHONY: test-build
test-build:
	sudo TEMPLATE_BUCKET_NAME=$(TEMPLATE_BUCKET_NAME) GOOGLE_SERVICE_ACCOUNT_BASE64=$(GOOGLE_SERVICE_ACCOUNT_BASE64) GCP_PROJECT_ID=$(GCP_PROJECT_ID) GCP_DOCKER_REPOSITORY_NAME=$(GCP_DOCKER_REPOSITORY_NAME) GCP_REGION=$(GCP_REGION) go run -race -gcflags=all="-N -l" main.go -test build -template d6a5c9wp4ccm7uqi4jzi -build 8e00bbdf-7f55-4025-9964-eede203c6ee5

.PHONY: test-delete
test-delete:
	sudo TEMPLATE_BUCKET_NAME=$(TEMPLATE_BUCKET_NAME) GOOGLE_SERVICE_ACCOUNT_BASE64=$(GOOGLE_SERVICE_ACCOUNT_BASE64) GCP_PROJECT_ID=$(GCP_PROJECT_ID) GCP_DOCKER_REPOSITORY_NAME=$(GCP_DOCKER_REPOSITORY_NAME) GCP_REGION=$(GCP_REGION) go run -race -gcflags=all="-N -l" main.go -test delete -env 0v0c9frk1etrhpxr5ljw

.PHONY: migrate
migrate:
	./upload-envs.sh /mnt/disks/fc-envs/v1 $(TEMPLATE_BUCKET_NAME)
