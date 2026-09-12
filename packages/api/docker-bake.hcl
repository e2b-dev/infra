// Builds the API server and its DB migrator in parallel.

variable "REGISTRY_PREFIX" {}

variable "COMMIT_SHA" {
  default = ""
}

variable "EXPECTED_MIGRATION_TIMESTAMP" {
  default = ""
}

// Comma-separated target platforms. The default keeps `make build-and-upload`
// a single-platform build for hosts without an arm64 emulator; the release
// workflow passes "linux/amd64,linux/arm64".
variable "PLATFORMS" {
  default = "linux/amd64"
}

group "default" {
  targets = ["api", "db-migrator"]
}

target "api" {
  context    = "."
  dockerfile = "api/Dockerfile"
  platforms  = split(",", PLATFORMS)
  tags       = concat(["${REGISTRY_PREFIX}/api"], COMMIT_SHA != "" ? ["${REGISTRY_PREFIX}/api:${COMMIT_SHA}"] : [])
  args = {
    COMMIT_SHA                   = COMMIT_SHA
    EXPECTED_MIGRATION_TIMESTAMP = EXPECTED_MIGRATION_TIMESTAMP
  }
}

target "db-migrator" {
  context    = "."
  dockerfile = "db/Dockerfile"
  platforms  = split(",", PLATFORMS)
  tags       = concat(["${REGISTRY_PREFIX}/db-migrator"], COMMIT_SHA != "" ? ["${REGISTRY_PREFIX}/db-migrator:${COMMIT_SHA}"] : [])
}
