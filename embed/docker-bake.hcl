// Builds the three project images: tools (Alpine + the host scripts), node-e2b
// (Node + the e2b SDK scripts) and seed (the local-dev database seeder).
// `make images` builds them locally under the names the two install files pin;
// the maintainers publish those same images into the registry repository the
// prefix below names.
//
// Bake must be invoked FROM this directory, the package root. `docker buildx
// bake` resolves a relative context or dockerfile against the CURRENT working
// directory, not against the bake file's own location, and the paths below are
// written for this directory, where `make images` runs.
//
// This default, the three pins in `compose/.env` and the three `newName`s
// in `kubernetes/kustomization.yaml` all name the same registry repository,
// so a rename has to change the seven together. `tests/pins.bats` reads this
// default and those six pins, and fails when they disagree.
variable "REGISTRY_PREFIX" {
  default = "us-docker.pkg.dev/e2b-artifacts/embed"
}
variable "COMMIT_SHA" {
  default = ""
}
// The seed builds from a source tree that has shared/, db/ and local-dev/
// side by side. In the source monorepo that tree sits three levels above this
// directory (the OSS tree that is exported as `packages/`); `make images`
// points these at the public runtime repository instead (context = that repo,
// SEED_SRC = packages).
variable "SEED_CONTEXT" {
  default = "../../.."
}
variable "SEED_SRC" {
  default = "go/oss"
}

group "default" {
  targets = ["tools", "node-e2b", "seed"]
}

target "tools" {
  context    = "compose"
  dockerfile = "docker/tools.Dockerfile"
  platforms  = ["linux/amd64"]
  tags       = concat(["${REGISTRY_PREFIX}/tools"], COMMIT_SHA != "" ? ["${REGISTRY_PREFIX}/tools:${COMMIT_SHA}"] : [])
}

target "node-e2b" {
  context    = "compose"
  dockerfile = "docker/node-e2b.Dockerfile"
  platforms  = ["linux/amd64"]
  tags       = concat(["${REGISTRY_PREFIX}/node-e2b"], COMMIT_SHA != "" ? ["${REGISTRY_PREFIX}/node-e2b:${COMMIT_SHA}"] : [])
}

target "seed" {
  context   = SEED_CONTEXT
  platforms = ["linux/amd64"]
  args = {
    SRC = SEED_SRC
  }
  dockerfile-inline = <<-DOCKERFILE
    FROM golang:1.26.8-alpine3.24
    # The pinned tag freezes the Alpine packages at whatever the base image was
    # built with, and this stage is also the published image; pull the release
    # branch's current ones in. Bump the date to force a rebuild from here
    # down, so that upgrade resolves against the current package index.
    ENV LAST_FORCED_UPDATE=2026-09-11
    RUN apk upgrade --no-cache
    ARG SRC=go/oss
    WORKDIR /src
    COPY $${SRC}/shared ./shared
    COPY $${SRC}/db ./db
    COPY $${SRC}/local-dev ./local-dev
    WORKDIR /src/local-dev
    RUN --mount=type=cache,target=/root/.cache/go-build --mount=type=cache,target=/go/pkg/mod \
        go build -o /seed ./seed-local-database.go
    ENTRYPOINT ["/seed"]
  DOCKERFILE
  tags = concat(["${REGISTRY_PREFIX}/seed"], COMMIT_SHA != "" ? ["${REGISTRY_PREFIX}/seed:${COMMIT_SHA}"] : [])
}
