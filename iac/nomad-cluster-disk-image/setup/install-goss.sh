#!/bin/bash
# Install goss - a server validation tool used for composite health checks.
# https://github.com/goss-org/goss

set -e

readonly SCRIPT_NAME="$(basename "$0")"
readonly DEFAULT_INSTALL_PATH="/usr/local/bin"
readonly DOWNLOAD_URL_BASE="https://github.com/goss-org/goss/releases/download"

function log_info {
  echo "===> $SCRIPT_NAME: $1"
}

function log_error {
  echo "ERROR $SCRIPT_NAME: $1" >&2
}

function print_usage {
  echo
  echo "Usage: install-goss [OPTIONS]"
  echo
  echo "Options:"
  echo
  echo -e "  --version\t\tThe version of goss to install. Required."
  echo
  echo "Example:"
  echo
  echo "  install-goss --version 0.4.9"
}

function install {
  local version=""

  while [[ $# -gt 0 ]]; do
    local key="$1"
    case "$key" in
      --version)
        version="$2"
        shift 2
        ;;
      --help)
        print_usage
        exit
        ;;
      *)
        log_error "Unrecognized argument: $key"
        print_usage
        exit 1
        ;;
    esac
  done

  if [[ -z "$version" ]]; then
    log_error "--version is required"
    print_usage
    exit 1
  fi

  local readonly url="${DOWNLOAD_URL_BASE}/v${version}/goss-linux-amd64"
  local readonly dest="${DEFAULT_INSTALL_PATH}/goss"

  log_info "Installing goss v${version} from ${url}"

  sudo curl -fsSL "$url" -o "$dest"
  sudo chmod +x "$dest"

  if command -v goss &>/dev/null; then
    log_info "goss install complete: $(goss --version)"
  else
    log_error "Could not find goss command after install. Aborting."
    exit 1
  fi
}

install "$@"
