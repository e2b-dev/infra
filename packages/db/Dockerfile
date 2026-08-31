ARG GOLANG_VERSION=1.26.5
ARG ALPINE_VERSION=3.24

# Builder stage
FROM golang:${GOLANG_VERSION}-alpine${ALPINE_VERSION} AS builder


ENV GOPROXY=https://goproxy.cn,direct
ENV GOSUMDB=sum.golang.google.cn
RUN sed -i 's/dl-cdn.alpinelinux.org/mirrors.aliyun.com/g' /etc/apk/repositories


# Cached golang dependencies
WORKDIR /build/shared
COPY ./shared/go.mod ./shared/go.sum ./
RUN go mod download

WORKDIR /build/db
COPY ./db/go.mod ./db/go.sum ./
RUN go mod download

# Copy source code
WORKDIR /build

COPY ./shared/pkg ./shared/pkg

COPY ./db/scripts/migrator.go ./db/scripts/migrator.go
COPY ./db/migrations/ ./db/migrations

WORKDIR /build/db

RUN go build -o ./bin/migrator ./scripts/migrator.go
RUN chmod +x ./bin/migrator

FROM alpine:${ALPINE_VERSION}

COPY --from=builder /build/db/bin/migrator .
COPY --from=builder  /build/db/migrations ./migrations

ENTRYPOINT ["./migrator"]
