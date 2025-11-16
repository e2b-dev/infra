# Orchestrator

## Commands

### Copy Build

> Works only for GCP buckets right now.

```bash
go run cmd/copy-build/main.go -build <build-id> -from <from-bucket> -to <to-bucket>
```

### Mount Rootfs

> We need root permissions to use NBD, so we cannot use `go run` directly, but we also need GCP credentials to access the template bucket.

```bash
./cmd/mount-rootfs/start.sh <bucket> <build-id>
```
