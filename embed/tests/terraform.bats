#!/usr/bin/env bats

# What the module says, not whether it parses: `make lint` runs fmt, init
# without a backend and validate over both the module and the example, and is
# the single lint path for them. These tests read the files, so they need no
# terraform on PATH.

setup() {
  cd "$BATS_TEST_DIRNAME/../terraform/gcp" || return 1
}

@test "the two shipped files are embedded from this directory, not fetched by default" {
  # -F: the literals contain `$`, which macOS grep reads as an anchor.
  # shellcheck disable=SC2016  # the ${path.module} is Terraform's, matched literally with -F
  grep -qF 'file("${path.module}/../../compose/compose.yaml")' main.tf
  # shellcheck disable=SC2016
  grep -qF 'file("${path.module}/../../compose/.env")' main.tf
  # The template assembles the URL from $MD, so match its parts.
  grep -q 'computeMetadata/v1/instance/attributes' startup.sh.tftpl
  grep -q 'docker compose up -d --wait' startup.sh.tftpl
  # Each metadata key is written in main.tf and read in the template, so a
  # rename in one file alone leaves the boot fetching a key nothing wrote.
  for key in e2b-compose-yaml e2b-dot-env; do
    grep -qF "$key" main.tf || { echo "$key is not in main.tf"; return 1; }
    grep -qF "$key" startup.sh.tftpl || { echo "$key is not in startup.sh.tftpl"; return 1; }
  done
}

@test "the startup script never prints the secrets it writes" {
  # The three secrets reach the instance in its .env, and the startup script's
  # output is the serial console and Cloud Logging, both readable with far less
  # than the project access the state file needs. `compose config` renders the
  # interpolated file, `set -x` traces every assignment and a plain cat of the
  # .env prints all three, so none of them may appear either.
  run grep -nE 'docker compose logs|docker compose config|set -x|cat[[:space:]]+\.env' startup.sh.tftpl
  [ "$status" -eq 1 ]
}

# The startup script deletes four keys from the shipped .env and appends its
# own, which is what puts a Terraform install on the secrets in its state and
# the sizing its variables ask for. compose.yaml reads each of them as
# ${KEY:-...}, so renaming one there and not here would silently drop the
# appended line: the file still parses, the stack still starts, and the
# install runs on what compose does when nothing is set -- its own generated
# team key and api secrets, which the state does not know, or the hugepage
# default rather than the requested one.
@test "the startup script writes only .env keys compose.yaml reads" {
  keys="$(awk '/^  cat >> \.env <<EOF$/ { f = 1; next } f && /^EOF$/ { exit } f' \
    startup.sh.tftpl | sed -n 's/^\([A-Z_][A-Z0-9_]*\)=.*/\1/p')"
  # An anchor that stopped matching would pass the test vacuously.
  [ -n "$keys" ]

  # The sed just above the heredoc strips the shipped value of each key before
  # the override is appended. The two lists have to be the same: a key appended
  # but not stripped leaves the shipped default sitting above the override in
  # the same file, and a key stripped but not appended drops it altogether.
  deleted="$(grep -F 'sed -i -E' startup.sh.tftpl |
    grep -oE '\([A-Z0-9_|]+\)' | tr -d '()' | tr '|' '\n' | sort)"
  [ -n "$deleted" ]
  diff <(printf '%s\n' "$deleted") <(printf '%s\n' "$keys" | sort) || {
    echo "the sed delete list (-) and the appended keys (+) differ"
    return 1
  }

  while read -r key; do
    # -F: the literal contains `$`, which macOS grep reads as an anchor.
    grep -qF "\${$key" ../../compose/compose.yaml || {
      echo "the startup script appends $key, which compose.yaml never reads"
      return 1
    }
  done <<<"$keys"
}
