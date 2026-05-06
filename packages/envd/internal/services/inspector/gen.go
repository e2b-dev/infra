// Package inspector — code generation directives for the eBPF
// filesystem net-change tracker.
//
// Running `go generate ./...` from packages/envd will invoke bpf2go,
// which compiles bpf/fs_tracker.bpf.c with clang and emits:
//
//   fstracker_bpfel.go   (linux/amd64+arm64+...) — embeds the BPF
//                         object as a byte slice and exposes typed
//                         loaders.
//
// Build prerequisites for `go generate`:
//   - clang >= 10
//   - libbpf headers (vendored under bpf/headers)
//
// The envd binary itself does NOT need clang at compile time; the
// generated _bpfel.go file embeds the compiled object.

package inspector

//go:generate bpf2go -cc clang -target bpfel,bpfeb -tags inspector_bpf -output-dir . -cflags "-O2 -g -Wall -D__TARGET_ARCH_x86 -I./bpf/headers -I/usr/include/x86_64-linux-gnu" fstracker ./bpf/fs_tracker.bpf.c
