const std = @import("std");

pub fn build(b: *std.Build) void {
    // Static, libc-free linux build with the smallest footprint. Override the
    // target with -Dtarget; the listen port with -Dport.
    const target = b.standardTargetOptions(.{ .default_target = .{
        .cpu_arch = .x86_64,
        .os_tag = .linux,
    } });

    const port = b.option(u16, "port", "TCP port the agent listens on") orelse 49982;
    const cgroup_root = b.option([]const u8, "cgroup_root", "cgroup2 mount root") orelse "/sys/fs/cgroup";
    const opts = b.addOptions();
    opts.addOption(u16, "port", port);
    opts.addOption([]const u8, "cgroup_root", cgroup_root);

    const exe = b.addExecutable(.{
        .name = "envd-init",
        .root_module = b.createModule(.{
            .root_source_file = b.path("src/main.zig"),
            .target = target,
            .optimize = .ReleaseSmall,
            .strip = true,
            .single_threaded = true,
            .unwind_tables = .none,
            .omit_frame_pointer = true,
            .error_tracing = false,
            .stack_check = false,
            .stack_protector = false,
            .imports = &.{
                .{ .name = "build_options", .module = opts.createModule() },
            },
        }),
    });
    b.installArtifact(exe);

    const run_cmd = b.addRunArtifact(exe);
    if (b.args) |args| run_cmd.addArgs(args);
    const run_step = b.step("run", "Run envd-init");
    run_step.dependOn(&run_cmd.step);

    const unit_tests = b.addTest(.{
        .root_module = b.createModule(.{
            .root_source_file = b.path("src/main.zig"),
            .target = b.graph.host,
            .imports = &.{
                .{ .name = "build_options", .module = opts.createModule() },
            },
        }),
    });
    const test_step = b.step("test", "Run unit tests");
    test_step.dependOn(&b.addRunArtifact(unit_tests).step);
}
