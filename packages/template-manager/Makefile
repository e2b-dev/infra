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
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o bin/template-manager .

.PHONY: build-debug
build-debug:
	CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build -race -gcflags=all="-N -l" -o bin/template-manager .

.PHONY: upload
upload:
	./upload.sh $(GCP_PROJECT_ID)

.PHONY: build-and-upload
build-and-upload: build upload

.PHONY: test-build
test-build:
	sudo go run -race -gcflags=all="-N -l" main.go -test build -template 0v0c9frk1etrhpxr5ljw -build 3351eada-8633-4e77-a757-6f869abd17c2

.PHONY: test-delete
test-delete:
	sudo go run -race -gcflags=all="-N -l" main.go -test delete -env 0v0c9frk1etrhpxr5ljw
