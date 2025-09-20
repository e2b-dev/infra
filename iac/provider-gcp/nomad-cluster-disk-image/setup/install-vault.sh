#!/bin/bash

set -e

# Import the appropriate bash commons libraries
readonly BASH_COMMONS_DIR="/opt/gruntwork/bash-commons"
readonly SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

readonly DEFAULT_INSTALL_PATH="/opt/vault"
readonly DEFAULT_VAULT_USER="vault"
readonly DOWNLOAD_PACKAGE_PATH="/tmp/vault.zip"
readonly SYSTEM_BIN_DIR="/usr/local/bin"

if [[ ! -d "$BASH_COMMONS_DIR" ]]; then
  echo "ERROR: this script requires that bash-commons is installed in $BASH_COMMONS_DIR. See https://github.com/gruntwork-io/bash-commons for more info."
  exit 1
fi

source "$BASH_COMMONS_DIR/assert.sh"
source "$BASH_COMMONS_DIR/log.sh"
source "$BASH_COMMONS_DIR/os.sh"

function print_usage {
  echo
  echo "Usage: install-vault [OPTIONS]"
  echo
  echo "This script can be used to install Vault and its dependencies. This script has been tested with Ubuntu 22.04."
  echo
  echo "Options:"
  echo
  echo -e "  --version\t\tThe version of Vault to install. Required."
  echo -e "  --path\t\tThe path where Vault should be installed. Optional. Default: $DEFAULT_INSTALL_PATH."
  echo -e "  --user\t\tThe user who will own the Vault install directories. Optional. Default: $DEFAULT_VAULT_USER."
  echo
  echo "Example:"
  echo
  echo "  install-vault --version 1.14.8"
}

function install_dependencies {
  log_info "Installing dependencies"
  sudo apt-get update -y
  sudo apt-get install -y curl unzip jq
}

function create_vault_user {
  local -r username="$1"

  if $(user_exists "$username"); then
    log_info "User $username already exists. Will not create again."
  else
    log_info "Creating user named $username"
    sudo useradd "$username" --system --home /nonexistent --shell /bin/false
  fi
}

function create_vault_install_paths {
  local -r path="$1"
  local -r username="$2"

  log_info "Creating install dirs for Vault at $path"
  sudo mkdir -p "$path"
  sudo mkdir -p "$path/bin"
  sudo mkdir -p "$path/config"
  sudo mkdir -p "$path/data"
  sudo mkdir -p "$path/tls"
  sudo mkdir -p "$path/log"

  log_info "Changing ownership of $path to $username"
  sudo chown -R "$username:$username" "$path"
}

function install_binaries {
  local -r version="$1"
  local -r path="$2"
  local -r username="$3"

  local -r url="https://releases.hashicorp.com/vault/${version}/vault_${version}_linux_amd64.zip"
  local -r download_path="$DOWNLOAD_PACKAGE_PATH"
  local -r bin_dir="$path/bin"
  local -r vault_dest_path="$bin_dir/vault"
  local -r run_vault_dest_path="$bin_dir/run-vault"

  log_info "Downloading Vault $version from $url to $download_path"
  curl -o "$download_path" "$url"
  unzip -d /tmp "$download_path"

  log_info "Moving Vault binary to $vault_dest_path"
  sudo mv "/tmp/vault" "$vault_dest_path"
  sudo chown "$username:$username" "$vault_dest_path"
  sudo chmod a+x "$vault_dest_path"

  local -r symlink_path="$SYSTEM_BIN_DIR/vault"
  if [[ -f "$symlink_path" ]]; then
    log_info "Symlink $symlink_path already exists. Will not add again."
  else
    log_info "Adding symlink to $vault_dest_path in $symlink_path"
    sudo ln -s "$vault_dest_path" "$symlink_path"
  fi
}

function install {
  local version=""
  local path="$DEFAULT_INSTALL_PATH"
  local user="$DEFAULT_VAULT_USER"

  while [[ $# > 0 ]]; do
    local key="$1"

    case "$key" in
      --version)
        version="$2"
        shift
        ;;
      --path)
        path="$2"
        shift
        ;;
      --user)
        user="$2"
        shift
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

    shift
  done

  assert_not_empty "--version" "$version"

  log_info "Starting Vault install"

  install_dependencies
  create_vault_user "$user"
  create_vault_install_paths "$path" "$user"
  install_binaries "$version" "$path" "$user"

  log_info "Vault install complete!"
}

install "$@"
