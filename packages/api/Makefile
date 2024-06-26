ENV := $(shell cat ../../.last_used_env || echo "not-set")
-include ../../.env.${ENV}


openapi := ../../spec/openapi.yml
codegen := go run github.com/deepmap/oapi-codegen/cmd/oapi-codegen@latest

.PHONY: generate
generate:
	$(codegen) -old-config-style -generate gin --package api $(openapi) > internal/api/api.gen.go
	$(codegen) -old-config-style -generate types --package api $(openapi) > internal/api/types.gen.go
	$(codegen) -old-config-style -generate spec --package api $(openapi) > internal/api/spec.gen.go


.PHONY: build
build:
	CGO_ENABLED=0 go build -o bin/api .

.PHONY: build-debug
build-debug:
	CGO_ENABLED=1 go build -race -gcflags=all="-N -l" -o bin/api .

.PHONY: run
run:
	make build-debug
	POSTGRES_CONNECTION_STRING=$(POSTGRES_CONNECTION_STRING) \
	GOTRACEBACK=crash \
	GODEBUG=madvdontneed=1 \
	./bin/api --port 3000


# You run the parametrized command like this:
# make metric=heap interval=90 profiler
.PHONY: profiler
profiler:
	go tool pprof -http :9991 http://localhost:3000/debug/pprof/$(metric)?seconds=$(interval)\&timeout=120
