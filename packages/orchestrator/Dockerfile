FROM golang:1.21 as builder

WORKDIR /build/shared

COPY .shared/go.mod .shared/go.sum ./
RUN go mod download

COPY .shared/pkg pkg

WORKDIR /build/block-storage

COPY .block-storage/go.mod .block-storage/go.sum ./
RUN go mod download

COPY .block-storage/pkg pkg

WORKDIR /build/orchestrator

COPY go.mod go.sum ./
RUN go mod download

COPY main.go Makefile ./
COPY internal internal

RUN --mount=type=cache,target=/root/.cache/go-build make build-local

FROM scratch

COPY --from=builder /build/orchestrator/bin/orchestrator .
