ENV := $(shell cat ../../.last_used_env || echo "not-set")
-include ../../.env.${ENV}

openapi := ../../spec/openapi.yml
codegen := go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@latest

.PHONY: generate
generate:
	$(codegen) -old-config-style -generate client --package api $(openapi) > internal/api/client.gen.go
	$(codegen) -old-config-style -generate models --package api $(openapi) > internal/api/models.gen.go


.PHONY: build-debug
build-debug:
	CGO_ENABLED=1 go build -race -gcflags=all="-N -l" -o bin/debug/envd ${LDFLAGS}

.PHONY: test
test:
	TESTS_API_SERVER_URL=$(TESTS_API_SERVER_URL) \
	TESTS_SANDBOX_TEMPLATE_ID=$(TESTS_SANDBOX_TEMPLATE_ID) \
	TESTS_E2B_API_KEY=$(TESTS_E2B_API_KEY) \
	go test -v ./internal/tests/...
