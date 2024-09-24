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
	@protoc --go_out=../shared/pkg/grpc/orchestrator/ --go_opt=paths=source_relative --go-grpc_out=../shared/pkg/grpc/orchestrator/ --go-grpc_opt=paths=source_relative orchestrator.proto
	@echo "Done"

.PHONY: build
build:
	@cp -r ../shared .shared/
	@cp -r ../block-storage .block-storage/
	@docker build --platform linux/amd64 --output=bin .
	@rm -rf .shared/ .block-storage/

.PHONY: build-local
build-local:
	CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build -o bin/orchestrator .

.PHONY: build-debug
build-debug:
	CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build -race -gcflags=all="-N -l" -o bin/orchestrator .

.PHONY: upload
upload:
	./upload.sh $(GCP_PROJECT_ID)

.PHONY: build-and-upload
build-and-upload: build upload

.PHONY: test
test:
	sudo TEMPLATE_BUCKET_NAME="e2b-dev-fc-templates" NODE_ID="test-client-22" CONSUL_TOKEN=$(CONSUL_TOKEN) go run -race -gcflags=all="-N -l" main.go -env 0x5brrleaeg0pxeon4uh -instance testinstance -build "9dc30023-c2e5-4cb7-8f4d-5ae196627abd" -alive 7 -count 1
