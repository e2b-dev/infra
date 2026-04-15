# NBD "Double Reply" / EIO Bug — Root Cause Analysis

## The symptom

After deploys, some sandboxes fail cleanup with:
```
failed to cleanup sandbox: error flushing cow device: failed to fsync path: input/output error
```

Kernel dmesg shows `Double reply`, `Unexpected reply`, or `Dead connection` on NBD devices, always within 8-10 seconds of the device being connected, on freshly booted nodes with cold caches.

## The root cause

A bug in the Linux kernel's NBD driver (present in 6.8, fixed in 6.14) causes the NBD device to die when a WRITE request's data page send fails.

**The code path (kernel 6.8 `drivers/block/nbd.c`):**

1. The kernel sends a WRITE request to the Dispatch handler via `nbd_send_cmd`. The 28-byte header is sent first, then the data pages.
2. The header send succeeds — the Dispatch handler (`dispatch.go:186`) receives and starts processing it.
3. The data page send via `sock_xmit` fails with `-EAGAIN` (-11). This happens when the Unix domain socket buffer is full — under memory pressure post-deploy, `sock_alloc_send_pskb` can't allocate buffer pages.
4. `nbd_send_cmd` returns `-EAGAIN` (kernel line 655). **It does NOT save `nsock->pending` or `nsock->sent`** — the partial-send state is lost. Only the `was_interrupted` path (for `ERESTARTSYS`/`EINTR`) saves this state.
5. `nbd_handle_cmd` (kernel line 1058-1063) sees `-EAGAIN` and:
   - Calls `nbd_mark_nsock_dead` — shuts down the socket
   - Calls `nbd_requeue_cmd` — calls `blk_mq_requeue_request` — calls `blk_mq_put_driver_tag` — **frees the tag**
   - The `NBD_CMD_INFLIGHT` flag is never set (kernel line 1056 was skipped because `ret != 0`)
6. With 1 NBD connection (`path_direct.go:134`, feature flag `NBDConnectionsPerDevice = 1`), there is no fallback connection. The requeued request is dispatched to the dead socket — `find_fallback` returns -1 — EIO. **The device is dead.**
7. All subsequent I/O returns EIO. When the sandbox is cleaned up, `rootfs/nbd.go` calls `sync()` — `syscall.Fsync` on `/dev/nbdN` — gets the latched EIO — `"failed to fsync path: input/output error"`.

## Why "Double reply" appears (sometimes)

The Dispatch handler (`dispatch.go:262-328`) is still processing the original request (the header was received in step 2). After several seconds (cold-cache GCS fetch), it writes the response to the socket with the original handle (tag + cookie).

Meanwhile, the freed tag (step 5) may have been reused by a new request, which increments `cmd->cmd_cookie` (kernel `nbd_send_cmd` line 618). If the kernel's `recv_work` thread reads the old response from the socket buffer before exiting, it sees:

- Response cookie = N (from the original request)
- Current `cmd->cmd_cookie` = N+1 (from the new request that reused the tag)
- Mismatch — `"Double reply on req, cmd_cookie N+1, handle cookie N"`

Whether you get "Double reply" or just "Dead connection" depends on timing — whether `recv_work` reads the buffered response before or after the socket shutdown propagates.

## Why it happens post-deploy with cold caches

1. Fresh node boots, all template/diff caches empty
2. Many sandboxes start simultaneously, heavy filesystem WRITE I/O
3. The kernel sends NBD_CMD_WRITE requests (28-byte header + data pages) through the Unix domain socket
4. Under memory pressure from concurrent sandbox creation, `sock_alloc_send_pskb` in `unix_stream_sendmsg` fails to allocate socket buffer pages — returns `-EAGAIN`
5. The `-EAGAIN` triggers the buggy requeue path — device death

## Why 8-10 seconds (not 90 seconds)

The 90-second `ioTimeout` (`path_direct.go:35`) is irrelevant. The timeout never fires. The trigger is `-EAGAIN` from `sock_sendmsg` under buffer pressure, which happens within seconds of the device being connected under load. The 8-10 second gap between device connect and "Double reply" in dmesg is simply the time the Dispatch handler takes to process the original request via a cold-cache GCS fetch.


The fix comment in the kernel source:
> *"We've already sent header or part of data payload, have no choice but to set pending and schedule it in work. And we have to return BLK_STS_OK to block core, otherwise this same request may be re-dispatched with different tag, but our header has been sent out with old tag, and this way does confuse reply handling."*

## Reproduction

**C reproducer** (`testdata/reproduce_double_reply.c`): Creates an NBD device with 4KB socket buffers and issues 128KB writes. The small buffers force `sock_sendmsg` to fail with `-EAGAIN` during data page send, triggering the exact kernel code path. Confirmed on kernel 6.8.0-1045-gcp:

```
block nbd15: Send data failed (result -11)        <- -EAGAIN, the trigger
block nbd15: Request send failed, requeueing       <- buggy requeue path
block nbd15: Dead connection, failed to find a fallback  <- device dead
-> EIO to all writers
```

**Mock test** (`dispatch_protocol_test.go`): `TestDispatch_Kernel68_DoubleReplyExact` simulates the kernel's tag-reuse perspective by sending two requests with the same tag but different cookies. Confirms the Dispatch handler produces the exact cookie mismatch that the kernel logs as "Double reply".

## Mitigation options

| Option | Effect | Risk |
|--------|--------|------|
| **Upgrade kernel to 6.14+** | Eliminates the bug | Kernel upgrade process |
| **Increase `NBDConnectionsPerDevice` to 2+** | Provides fallback connection — device survives | May increase sandbox start times |
