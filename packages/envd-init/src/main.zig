// envd-init: a tiny static lifecycle agent that runs alongside envd and answers
// the orchestrator's resume-critical control calls (/health, /init) with a
// minimal, pinned memory footprint, so resuming a sandbox does not fault in the
// full Go envd. /init thaws the user/pty cgroups frozen before pause and sets
// the clock on meaningful skew; the rest of the payload stays with envd.

const std = @import("std");
const linux = std.os.linux;
const build_options = @import("build_options");

const page_size = 4096;
const read_buf_size = 64 * 1024;

// Single fixed, page-aligned working buffer (no heap, reused per connection).
// Pinned resident by pinArena so its pages don't fragment across resumes.
var arena: [read_buf_size]u8 align(page_size) = undefined;

const resp_204 = "HTTP/1.1 204 No Content\r\nConnection: close\r\nContent-Length: 0\r\nCache-Control: no-store\r\n\r\n";
const resp_400 = "HTTP/1.1 400 Bad Request\r\nConnection: close\r\nContent-Length: 0\r\n\r\n";
const resp_404 = "HTTP/1.1 404 Not Found\r\nConnection: close\r\nContent-Length: 0\r\n\r\n";

// Cgroups frozen by the orchestrator before pause (names mirror envd's).
const frozen_groups = [_][]const u8{ "user", "ptys" };

// Skew thresholds mirror envd's shouldSetSystemTime.
const max_past_ns: i64 = 50 * 1_000_000;
const max_future_ns: i64 = 5 * 1_000_000_000;

pub fn main() void {
    serve(build_options.port) catch |err| {
        writeAll(2, "envd-init: fatal: ");
        writeAll(2, @errorName(err));
        writeAll(2, "\n");
        linux.exit(1);
    };
}

const ServeError = error{ Socket, Bind, Listen };

fn serve(port: u16) ServeError!void {
    const sfd = linux.socket(linux.AF.INET, linux.SOCK.STREAM, 0);
    if (isErr(sfd)) return error.Socket;
    const sock: i32 = @intCast(sfd);

    const one: c_int = 1;
    _ = linux.setsockopt(sock, linux.SOL.SOCKET, linux.SO.REUSEADDR, std.mem.asBytes(&one), @sizeOf(c_int));

    const addr = linux.sockaddr.in{ .port = std.mem.nativeToBig(u16, port), .addr = 0 };
    if (isErr(linux.bind(sock, @ptrCast(&addr), @sizeOf(linux.sockaddr.in)))) return error.Bind;
    if (isErr(linux.listen(sock, 128))) return error.Listen;

    pinArena();
    logListening(port);

    while (true) {
        const cfd = linux.accept(sock, null, null);
        if (isErr(cfd)) continue;
        const conn: i32 = @intCast(cfd);
        handleConn(conn);
        _ = linux.close(conn);
    }
}

// pinArena prefaults and mlocks the arena so it is resident before any snapshot
// and never reclaimed or migrated. mlock is best-effort.
fn pinArena() void {
    var i: usize = 0;
    while (i < arena.len) : (i += page_size) arena[i] = 0;
    _ = linux.mlock(&arena, arena.len);
}

fn handleConn(conn: i32) void {
    const req = readRequest(conn, &arena) orelse {
        writeAll(conn, resp_400);
        return;
    };

    if (matchLine(req.head, "GET ", "/health")) {
        writeAll(conn, resp_204);
    } else if (matchLine(req.head, "POST ", "/init")) {
        applyInit(req.body);
        writeAll(conn, resp_204);
    } else {
        writeAll(conn, resp_404);
    }
}

const Request = struct {
    head: []const u8,
    body: []const u8,
};

// readRequest reads headers, then the declared Content-Length body, bounded by buf.
fn readRequest(conn: i32, buf: []u8) ?Request {
    var total: usize = 0;
    var header_end: ?usize = null;

    while (total < buf.len) {
        const rc = linux.read(conn, buf.ptr + total, buf.len - total);
        if (isErr(rc)) return null;
        if (rc == 0) break;
        total += rc;

        if (header_end == null) {
            if (std.mem.indexOf(u8, buf[0..total], "\r\n\r\n")) |idx| header_end = idx;
        }
        if (header_end) |he| {
            const body_start = he + 4;
            const want = contentLength(buf[0..he]);
            if (total - body_start >= want) {
                return .{ .head = buf[0..he], .body = buf[body_start .. body_start + want] };
            }
        }
    }

    const he = header_end orelse return null;
    return .{ .head = buf[0..he], .body = buf[@min(he + 4, total)..total] };
}

fn contentLength(head: []const u8) usize {
    const key = "content-length:";
    var line_start: usize = 0;
    while (line_start < head.len) {
        const rel = std.mem.indexOfPos(u8, head, line_start, "\r\n");
        const line = head[line_start .. rel orelse head.len];
        if (line.len >= key.len and asciiEqlIgnoreCase(line[0..key.len], key)) {
            return parseDigits(usize, std.mem.trim(u8, line[key.len..], " \t")) orelse 0;
        }
        if (rel == null) break;
        line_start = rel.? + 2;
    }
    return 0;
}

fn matchLine(head: []const u8, method: []const u8, path: []const u8) bool {
    if (!std.mem.startsWith(u8, head, method)) return false;
    const rest = head[method.len..];
    if (!std.mem.startsWith(u8, rest, path)) return false;
    if (rest.len == path.len) return true;
    return rest[path.len] == ' ' or rest[path.len] == '?';
}

fn applyInit(body: []const u8) void {
    if (extractJsonString(body, "timestamp")) |ts| {
        if (parseRfc3339Epoch(ts)) |host_secs| maybeSetRealtime(host_secs);
    }
    thawAt(build_options.cgroup_root);
}

fn thawAt(root: []const u8) void {
    for (frozen_groups) |group| {
        var buf: [256]u8 = undefined;
        const path = joinFreezePath(&buf, root, group) orelse continue;
        const rc = linux.openat(linux.AT.FDCWD, path, .{ .ACCMODE = .WRONLY }, 0);
        if (isErr(rc)) continue;
        const fd: i32 = @intCast(rc);
        _ = linux.write(fd, "0", 1);
        _ = linux.close(fd);
    }
}

fn joinFreezePath(buf: []u8, root: []const u8, group: []const u8) ?[*:0]const u8 {
    const suffix = "/cgroup.freeze";
    if (root.len + 1 + group.len + suffix.len + 1 > buf.len) return null;
    var i: usize = 0;
    @memcpy(buf[i .. i + root.len], root);
    i += root.len;
    buf[i] = '/';
    i += 1;
    @memcpy(buf[i .. i + group.len], group);
    i += group.len;
    @memcpy(buf[i .. i + suffix.len], suffix);
    i += suffix.len;
    buf[i] = 0;
    return @ptrCast(buf.ptr);
}

fn maybeSetRealtime(host_secs: i64) void {
    var now: linux.timespec = undefined;
    if (isErr(linux.clock_gettime(.REALTIME, &now))) {
        setRealtime(host_secs);
        return;
    }
    const guest_ns = @as(i64, now.sec) * 1_000_000_000 + @as(i64, now.nsec);
    if (shouldSetTime(guest_ns, host_secs * 1_000_000_000)) setRealtime(host_secs);
}

fn shouldSetTime(guest_ns: i64, host_ns: i64) bool {
    return guest_ns < host_ns - max_past_ns or guest_ns > host_ns + max_future_ns;
}

fn setRealtime(secs: i64) void {
    const ts = linux.timespec{ .sec = @intCast(secs), .nsec = 0 };
    _ = linux.clock_settime(.REALTIME, &ts);
}

// extractJsonString scans for "key":"value" in the flat /init payload.
fn extractJsonString(body: []const u8, comptime key: []const u8) ?[]const u8 {
    const pat = "\"" ++ key ++ "\"";
    const ki = std.mem.indexOf(u8, body, pat) orelse return null;
    var i = ki + pat.len;
    while (i < body.len and (body[i] == ' ' or body[i] == ':')) : (i += 1) {}
    if (i >= body.len or body[i] != '"') return null;
    i += 1;
    const start = i;
    while (i < body.len and body[i] != '"') : (i += 1) {}
    if (i >= body.len) return null;
    return body[start..i];
}

// parseRfc3339Epoch parses the leading YYYY-MM-DDTHH:MM:SS as UTC seconds,
// ignoring fractional seconds and zone (skew tolerance dwarfs them).
fn parseRfc3339Epoch(s: []const u8) ?i64 {
    if (s.len < 19) return null;
    const year = parseFixed(s[0..4]) orelse return null;
    const month = parseFixed(s[5..7]) orelse return null;
    const day = parseFixed(s[8..10]) orelse return null;
    const hour = parseFixed(s[11..13]) orelse return null;
    const min = parseFixed(s[14..16]) orelse return null;
    const sec = parseFixed(s[17..19]) orelse return null;
    if (s[4] != '-' or s[7] != '-' or (s[10] != 'T' and s[10] != ' ') or s[13] != ':' or s[16] != ':') return null;
    return civilToDays(year, month, day) * 86400 + hour * 3600 + min * 60 + sec;
}

fn parseFixed(s: []const u8) ?i64 {
    return parseDigits(i64, s);
}

fn parseDigits(comptime T: type, s: []const u8) ?T {
    if (s.len == 0) return null;
    var v: T = 0;
    for (s) |c| {
        if (c < '0' or c > '9') return null;
        const m = @mulWithOverflow(v, 10);
        const a = @addWithOverflow(m[0], @as(T, c - '0'));
        if (m[1] != 0 or a[1] != 0) return null;
        v = a[0];
    }
    return v;
}

// civilToDays: days since 1970-01-01 (Howard Hinnant's algorithm).
fn civilToDays(y_in: i64, m: i64, d: i64) i64 {
    const y = if (m <= 2) y_in - 1 else y_in;
    const era = @divFloor(if (y >= 0) y else y - 399, 400);
    const yoe = y - era * 400;
    const doy = @divFloor(153 * (if (m > 2) m - 3 else m + 9) + 2, 5) + d - 1;
    const doe = yoe * 365 + @divFloor(yoe, 4) - @divFloor(yoe, 100) + doy;
    return era * 146097 + doe - 719468;
}

fn writeAll(fd: i32, bytes: []const u8) void {
    var off: usize = 0;
    while (off < bytes.len) {
        const rc = linux.write(fd, bytes.ptr + off, bytes.len - off);
        if (isErr(rc) or rc == 0) return;
        off += rc;
    }
}

fn asciiEqlIgnoreCase(a: []const u8, b: []const u8) bool {
    if (a.len != b.len) return false;
    for (a, b) |ca, cb| {
        if (std.ascii.toLower(ca) != std.ascii.toLower(cb)) return false;
    }
    return true;
}

// isErr reports whether a raw linux syscall return encodes -errno.
fn isErr(rc: usize) bool {
    return @as(isize, @bitCast(rc)) < 0;
}

fn logListening(port: u16) void {
    var buf: [8]u8 = undefined;
    var i: usize = buf.len;
    var n: u16 = port;
    while (true) {
        i -= 1;
        buf[i] = '0' + @as(u8, @intCast(n % 10));
        n /= 10;
        if (n == 0) break;
    }
    writeAll(2, "envd-init: listening on port ");
    writeAll(2, buf[i..]);
    writeAll(2, "\n");
}

const testing = std.testing;

test "parseRfc3339Epoch" {
    try testing.expectEqual(@as(?i64, 1893553445), parseRfc3339Epoch("2030-01-02T03:04:05Z"));
    try testing.expectEqual(@as(?i64, 1893553445), parseRfc3339Epoch("2030-01-02T03:04:05.123Z"));
    try testing.expectEqual(@as(?i64, 0), parseRfc3339Epoch("1970-01-01T00:00:00Z"));
    try testing.expectEqual(@as(?i64, null), parseRfc3339Epoch("bogus"));
}

test "thawAt writes 0 to each group's cgroup.freeze" {
    const root = ".ei-test-cg";
    cleanupTree(root);
    defer cleanupTree(root);
    try expectOk(linux.mkdir(root, 0o755));

    inline for (frozen_groups) |g| {
        var b: [256]u8 = undefined;
        try expectOk(linux.mkdir(try std.fmt.bufPrintZ(&b, "{s}/{s}", .{ root, g }), 0o755));
        try writeFileRaw(try std.fmt.bufPrintZ(&b, "{s}/{s}/cgroup.freeze", .{ root, g }), "1");
    }

    thawAt(root);

    inline for (frozen_groups) |g| {
        var b: [256]u8 = undefined;
        const fp = try std.fmt.bufPrintZ(&b, "{s}/{s}/cgroup.freeze", .{ root, g });
        var out: [4]u8 = undefined;
        try testing.expectEqualStrings("0", try readFileRaw(fp, &out));
    }
}

fn expectOk(rc: usize) !void {
    if (isErr(rc)) return error.Syscall;
}

fn writeFileRaw(path: [*:0]const u8, data: []const u8) !void {
    const rc = linux.openat(linux.AT.FDCWD, path, .{ .ACCMODE = .WRONLY, .CREAT = true, .TRUNC = true }, 0o644);
    if (isErr(rc)) return error.Open;
    const fd: i32 = @intCast(rc);
    defer _ = linux.close(fd);
    _ = linux.write(fd, data.ptr, data.len);
}

fn readFileRaw(path: [*:0]const u8, buf: []u8) ![]u8 {
    const rc = linux.openat(linux.AT.FDCWD, path, .{ .ACCMODE = .RDONLY }, 0);
    if (isErr(rc)) return error.Open;
    const fd: i32 = @intCast(rc);
    defer _ = linux.close(fd);
    const n = linux.read(fd, buf.ptr, buf.len);
    if (isErr(n)) return error.Read;
    return buf[0..n];
}

fn cleanupTree(root: []const u8) void {
    inline for (frozen_groups) |g| {
        var b: [256]u8 = undefined;
        if (std.fmt.bufPrintZ(&b, "{s}/{s}/cgroup.freeze", .{ root, g })) |fp| {
            _ = linux.unlinkat(linux.AT.FDCWD, fp, 0);
        } else |_| {}
        if (std.fmt.bufPrintZ(&b, "{s}/{s}", .{ root, g })) |dir| {
            _ = linux.unlinkat(linux.AT.FDCWD, dir, linux.AT.REMOVEDIR);
        } else |_| {}
    }
    var rb: [256]u8 = undefined;
    if (std.fmt.bufPrintZ(&rb, "{s}", .{root})) |r| {
        _ = linux.unlinkat(linux.AT.FDCWD, r, linux.AT.REMOVEDIR);
    } else |_| {}
}
