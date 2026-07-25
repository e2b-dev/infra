# E2B premade NixOS sandbox base (FEAT-145 / qa.md QA13 "nixos" tier).
# Everything provision.sh does imperatively on other distros is declared here;
# the orchestrator's nixos profile only verifies and boots.
{ config, pkgs, lib, ... }:
{
  # Boot: the E2B microVM supplies its own kernel and mounts the rootfs rw
  # (root=/dev/vda rw), then runs this system's stage-2 init directly — no
  # initrd, no bootloader.
  # The initrd/kernel in the closure are unused dead weight (the microVM
  # boots E2B's kernel with init= pointing at this system's stage-2 init),
  # but NixOS's module system requires them to evaluate; only grub is off.
  boot.loader.grub.enable = false;
  fileSystems."/" = { device = "/dev/vda"; fsType = "ext4"; };

  # eth0 is configured by the kernel command line (ip=...); nothing to manage.
  networking.useDHCP = false;
  networking.resolvconf.enable = false;
  # The E2B rootfs layer bakes an immutable /etc/resolv.conf; NixOS must not
  # try to regenerate it at activation.
  environment.etc."resolv.conf".enable = false;

  # The env daemon: E2B bakes the static binary at /usr/bin/envd as an OCI
  # layer on top of this image. Mirrors envd.service.tpl (systemd family).
  systemd.services.envd = {
    description = "E2B env daemon";
    wantedBy = [ "multi-user.target" ];
    unitConfig.StartLimitIntervalSec = 0;
    serviceConfig = {
      Type = "simple";
      Restart = "always";
      ExecStart = "/usr/bin/envd";
      Environment = "GOTRACEBACK=all";
      OOMPolicy = "continue";
      OOMScoreAdjust = -1000;
    };
  };

  # Default sandbox user (matches configure.sh on the other families).
  # Match useradd semantics on the other families: a per-user group named
  # after the user (configure.sh chowns /home/user to user:user).
  users.groups.user = {};
  users.users.user = {
    isNormalUser = true;
    group = "user";
    extraGroups = [ "wheel" ];
    initialHashedPassword = "";
  };
  users.users.root.initialHashedPassword = "";
  security.sudo.wheelNeedsPassword = false;
  # The DEFAULT USER build step greps for this exact line before appending to
  # /etc/sudoers (which is a read-only store symlink on NixOS) — declaring it
  # makes that step a clean no-op.
  security.sudo.extraConfig = "user ALL=(ALL:ALL) NOPASSWD: ALL";

  services.openssh = {
    enable = true;
    settings.PermitRootLogin = "yes";
    settings.PermitEmptyPasswords = true;
  };
  security.pam.services.sshd.allowNullPassword = true;

  services.chrony = {
    enable = true;
    servers = [ "pool.ntp.org" ];
    extraConfig = "makestep 1.0 3";
  };

  # Journald must not watchdog-reboot when the microVM is paused for
  # snapshots (mirrors the systemd-family override baked into other images).
  systemd.services.systemd-journald.serviceConfig.WatchdogSec = 0;

  # No serial getty fighting the console; keep the closure lean.
  systemd.services."serial-getty@ttyS0".enable = false;
  documentation.enable = false;

  # Kernel tunables provision.sh writes to /etc/sysctl.conf on other
  # families (NixOS reads sysctl.d from its own config instead).
  boot.kernel.sysctl."fs.inotify.max_user_watches" = 65536;
  boot.kernel.sysctl."vm.compaction_proactiveness" = 0;

  # E2B build steps and customer commands are executed via /bin/bash (the
  # orchestrator invokes it explicitly, like on every FHS distro) — provide it.
  system.activationScripts.e2bBinBash = "mkdir -m 0755 -p /bin && ln -sfn ${pkgs.bash}/bin/bash /bin/bash";

  system.stateVersion = "24.05";
}
