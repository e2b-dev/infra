package reconcile

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// RunReconcile scans common Firecracker artifact locations and iptables
// and writes a plain-text report to stdout and to /tmp/reconcile-report-<ts>.txt.
// injectable vars to make the package testable
var (
	SocketDirs  = []string{"/data0/tmp", "/tmp"}
	MetricsDir  = "/data0/tmp"
	ExecCommand = exec.Command
)

func RunReconcile(outPath string) error {
	ts := time.Now().UTC().Format("20060102T150405Z")
	if outPath == "" {
		outPath = fmt.Sprintf("/tmp/reconcile-report-%s.txt", ts)
	}

	var buf bytes.Buffer
	fmt.Fprintf(&buf, "Reconcile sweep report\nTimestamp: %s\n\n", ts)

	// scan socket dirs
	fmt.Fprintln(&buf, "==== Firecracker api sockets ====")
	for _, d := range SocketDirs {
		scanAndWrite(&buf, d, "fc-*.sock")
	}

	fmt.Fprintln(&buf, "\n==== uffd sockets ====")
	for _, d := range SocketDirs {
		scanAndWrite(&buf, d, "uffd-*.sock")
	}

	fmt.Fprintln(&buf, "\n==== metrics FIFOs ====")
	scanAndWrite(&buf, MetricsDir, "fc-metrics-*")

	fmt.Fprintln(&buf, "\n==== iptables (nat table) ====")
	if out, err := ExecCommand("iptables-save", "-t", "nat").CombinedOutput(); err != nil {
		fmt.Fprintf(&buf, "iptables-save error: %v\n", err)
	} else {
		buf.Write(out)
	}

	// write report
	if err := ioutil.WriteFile(outPath, buf.Bytes(), 0644); err != nil {
		return fmt.Errorf("write report: %w", err)
	}

	// also echo to stdout
	_, _ = os.Stdout.Write(buf.Bytes())
	fmt.Fprintf(os.Stdout, "\nReport written to: %s\n", outPath)
	return nil
}

func scanAndWrite(buf *bytes.Buffer, dir, pattern string) {
	files, err := filepath.Glob(filepath.Join(dir, pattern))
	if err != nil {
		fmt.Fprintf(buf, "scan %s/%s error: %v\n", dir, pattern, err)
		return
	}
	if len(files) == 0 {
		fmt.Fprintf(buf, "(none found) %s/%s\n", dir, pattern)
		return
	}
	for _, f := range files {
		fi, err := os.Lstat(f)
		if err != nil {
			fmt.Fprintf(buf, "%s (stat error: %v)\n", f, err)
			continue
		}
		fmt.Fprintf(buf, "%s  %s\n", f, fi.Mode().String())
	}
}
