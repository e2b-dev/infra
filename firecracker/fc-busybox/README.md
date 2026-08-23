# fc-busybox

BusyBox static builds for [E2B](https://e2b.dev) sandbox VMs.

## What this builds

A BusyBox binary with default config (all standard applets including ash shell), statically linked with musl libc for both **amd64** and **arm64**.

The sandbox init script uses `#!/usr/bin/busybox ash`, so ash and the full default applet set must be present.

## Build

```bash
sudo ./build.sh                          # host arch
sudo TARGET_ARCH=arm64 ./build.sh        # arm64
sudo BUSYBOX_VERSION=1.37.0 ./build.sh   # custom version
```

Output: `builds/{version}/{arch}/busybox`
