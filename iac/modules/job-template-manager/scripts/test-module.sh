#!/usr/bin/env bash

set -euo pipefail

terraform_bin="${1:-terraform}"
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
module_dir="$(cd "${script_dir}/.." && pwd)"
provider_dir="$(cd "${module_dir}/../../provider-gcp" && pwd)"
fixture_dir="$(mktemp -d)"
trap 'rm -rf -- "${fixture_dir}"' EXIT HUP INT TERM

cp -R "${module_dir}/." "${fixture_dir}/"
cp "${provider_dir}/.terraform.lock.hcl" "${fixture_dir}/.terraform.lock.hcl"

"${terraform_bin}" -chdir="${fixture_dir}" init \
  -backend=false \
  -input=false \
  >/dev/null
"${terraform_bin}" -chdir="${fixture_dir}" test
