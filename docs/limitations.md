Custom template build: Debian/Ubuntu-only base images
===================================================

Summary
-------

E2B currently requires that base images used for custom template builds are Debian/Ubuntu-based and provide the `apt` package manager. The template build and provisioning scripts assume Debian package manager behavior and Debian-style filesystem layouts; using images without `apt` (for example, Alpine, CentOS, RHEL, or other non-Debian distributions) will cause the build to fail.

Why this limitation exists
-------------------------

- The provisioning scripts used during template build call `apt` and expect Debian-specific package names and file locations.
- Debian/Ubuntu images use a filesystem layout and package tooling the scripts rely upon.

Workarounds and contribution
---------------------------

There is no known workaround at the moment. If you need non-Debian support, please open an issue describing the use case or submit a PR that updates the provisioning scripts to support alternative package managers (e.g., `apk`, `yum`, `dnf`) and corresponding filesystem differences.

Suggested PR wording
--------------------

Add a short note in the docs: "Custom template builds require Debian/Ubuntu-based base images (apt). Non-Debian images (Alpine, CentOS/RHEL, etc.) are not supported and will fail during the build process. Contributions to add non-Debian support are welcome; please open an issue or PR."
