# Sandbox-Manager Changelog

## v0.4.0 (2026-05-10) — MX.10: Persistent VMs via TAP+SSH

**BREAKING CHANGE:** ephemeral-per-cmd mode replaced with persistent VM cold-boot + SSH-exec.

### New
- 10-slot TAP-pool (mxtap0..mxtap9, 172.16.<i*4>.0/30)
- Rootfs v3 with sshd + injected authorized_keys
- Cold-boot ~1.2s, per-exec ~180ms (SSH)
- Multi-step state preserved across tool-calls

### Files
- `setup-tap-pool.sh` — pre-create TAP devices + NAT (run on PRIMARY boot)
- `build-rootfs-v3-persist.sh` — build SSH-enabled rootfs
- `sandbox_manager.py` v0.4 — 10-slot TAP-pool sandbox-mgr

### Architecture
```
POST /v1/sandbox/create
  → claim free TAP slot (172.16.<i*4>.1/30 host)
  → cp rootfs template → per-VM overlay
  → spawn FC with PUT /machine-config + /boot-source + /drives + /network-interfaces
  → kernel cmdline: ip=<vm-ip>::<host-ip>:255.255.255.252::eth0:off
  → InstanceStart
  → wait for SSH (172.16.<i*4>.2:22) — max 5s
  → return {sandbox_id, vm_ip, boot_time_ms}

POST /v1/sandbox/{id}/exec
  → ssh root@<vm-ip> <cmd>
  → return {stdout, exit_code, exec_time_ms}

DELETE /v1/sandbox/{id}
  → kill FC process
  → cleanup overlay + socket
  → free TAP slot
```

## v0.3.0 (2026-05-10) — MX.5: ephemeral-exec

Initial release. Ephemeral fresh VM per /exec call.
