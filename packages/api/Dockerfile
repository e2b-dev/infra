FROM golang:1.23-alpine3.20 AS builder

RUN apk add --no-cache make

WORKDIR /build/shared

COPY .shared/go.mod .shared/go.sum ./
RUN go mod download

COPY .shared/pkg pkg

WORKDIR /build/api

COPY go.mod go.sum Makefile ./
RUN go mod download

COPY internal internal
COPY main.go main.go

ARG COMMIT_SHA
RUN --mount=type=cache,target=/root/.cache/go-build make build COMMIT_SHA=${COMMIT_SHA}

RUN chmod +x /build/api/bin/api

FROM alpine:3.17

COPY --from=builder /build/api/bin/api .

# Set Gin server to the production mode
ENV GIN_MODE=release
ENTRYPOINT [ "./api"]
