# envd-init is built with Zig (see build.zig.zon for the pinned minimum).
# Set ZIG to a 0.16.x toolchain if `zig` is not on PATH.
ZIG ?= zig

# Listen port; must match the orchestrator's envd-init control port.
PORT ?= 49982

.PHONY: build
build:
	$(ZIG) build -Dport=$(PORT)

.PHONY: test
test:
	$(ZIG) build test

.PHONY: fmt
fmt:
	$(ZIG) fmt src build.zig

.PHONY: lint
lint:
	$(ZIG) fmt --check src build.zig

.PHONY: clean
clean:
	rm -rf zig-out .zig-cache
