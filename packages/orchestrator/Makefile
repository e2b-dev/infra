ENV := $(shell cat ../../.last_used_env || echo "not-set")
-include ../../.env.${ENV}

client := gcloud compute instances list --format='csv(name)' --project $(GCP_PROJECT_ID) | grep "client"

OTEL_TRACING_PRINT ?= false

test_vars := POSTGRES_CONNECTION_STRING=$(POSTGRES_CONNECTION_STRING) \
	TEMPLATE_BUCKET_NAME=$(TEMPLATE_BUCKET_NAME) \
	CONSUL_TOKEN=$(CONSUL_TOKEN) \
	OTEL_TRACING_PRINT=$(OTEL_TRACING_PRINT) \
	NODE_ID="test-client"

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
	@rm -rf .shared/
	@cp -r ../shared .shared/
	@docker build --platform linux/amd64 --output=bin .
	@rm -rf .shared/

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

.PHONY: mock
mock:
	sudo TEMPLATE_BUCKET_NAME=$(TEMPLATE_BUCKET_NAME) CONSUL_TOKEN=$(CONSUL_TOKEN) NODE_ID="test-client" go run cmd/mock-sandbox/mock.go -template 5wzg6c91u51yaebviysf -build "f0370054-b669-eeee-b33b-573d5287c6ef" -alive 1 -count 2

.PHONY: mock-nbd
mock-nbd:
	sudo go run -gcflags=all="-N -l" cmd/mock-nbd/mock.go

.PHONY: killall
killall:
	gcloud compute instance-groups list-instances $(PREFIX)orch-client-ig \
          	  --zone=$(GCP_ZONE) \
          	  --project=$(GCP_PROJECT_ID) \
          	  --format="value(instance)" \
          	  --quiet | xargs -I {} -P 5 sh -c "gcloud compute ssh {} --project=$(GCP_PROJECT_ID) --zone=$(GCP_ZONE) --command='sudo killall -9 firecracker'"
	@echo "Killing all firecracker processes"

.PHONY: kill-old
kill-old:
	gcloud compute instance-groups list-instances $(PREFIX)orch-client-ig \
          	  --zone=$(GCP_ZONE) \
          	  --project=$(GCP_PROJECT_ID) \
          	  --format="value(instance)" \
          	  --quiet | xargs -I {} -P 5 sh -c "gcloud compute ssh {} --project=$(GCP_PROJECT_ID) --zone=$(GCP_ZONE) --command='sudo killall -9 --older-than 24h firecracker'"
	@echo "Killing all firecracker processes"

.PHONY: mock-snapshot
mock-snapshot:
	sudo TEMPLATE_BUCKET_NAME=$(TEMPLATE_BUCKET_NAME) CONSUL_TOKEN=$(CONSUL_TOKEN) NODE_ID="test-client" go run cmd/mock-snapshot/mock.go  -template 5wzg6c91u51yaebviysf -build "f0370054-b669-4d7e-b33b-573d5287c6ef" -alive 1 -count 1

.PHONY: mock-generate
mock-generate:
	go install github.com/vektra/mockery/v2@v2.52.1
	$$(go env GOPATH)/bin/mockery

.PHONY: test
test:
	@sudo $(test_vars) go test ./internal/sandbox/ -v

.PHONY: benchmark
benchmark:
	@sudo $(test_vars) go test -bench=. ./internal/sandbox/ -benchtime=5x