#!/bin/bash

REQUIRED_VERSION="2.1.6"
BIN_PATH="$(go env GOPATH)/bin/golangci-lint"

get_linter_version() {
    "$BIN_PATH" --version 2>/dev/null | awk '{print $4}'
}

if ! command -v golangci-lint >/dev/null 2>&1 || [[ "$(get_linter_version)" != "$REQUIRED_VERSION" ]]; then
    echo >&2 "golangci-lint not found or incorrect version. Installing v${REQUIRED_VERSION}..."
    curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/HEAD/install.sh | sh -s -- -b "$(go env GOPATH)/bin" "v${REQUIRED_VERSION}" || {
        echo >&2 "Installation failed."; exit 1;
    }
else
    echo "golangci-lint v${REQUIRED_VERSION} is already installed."
fi