.PHONY: build
build:
	$(eval COMMIT_SHA ?= $(shell git rev-parse --short HEAD))
	mkdir -p bin
	CGO_ENABLED=0 go build -o bin/code-executor -ldflags "-X=main.commitSHA=$(COMMIT_SHA)" .

.PHONY: build-debug
build-debug:
	$(eval COMMIT_SHA ?= $(shell git rev-parse --short HEAD))
	mkdir -p bin
	CGO_ENABLED=1 go build -race -gcflags=all="-N -l" -o bin/code-executor -ldflags "-X=main.commitSHA=$(COMMIT_SHA)" .

.PHONY: run
run:
	make build-debug
	./bin/code-executor --port 8080 --workers 10 --piston-url http://localhost:2000 --debug

.PHONY: test
test:
	go test -race -v ./...

.PHONY: deps
deps:
	go mod download
	go mod tidy

