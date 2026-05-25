package reconcile

import (
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunReconcile_TempDirs(t *testing.T) {
	tmp, err := ioutil.TempDir("", "reconcile-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)

	data0 := filepath.Join(tmp, "data0", "tmp")
	if err := os.MkdirAll(data0, 0755); err != nil {
		t.Fatal(err)
	}
	localtmp := filepath.Join(tmp, "localtmp")
	if err := os.MkdirAll(localtmp, 0755); err != nil {
		t.Fatal(err)
	}

	// create sample socket and metric files
	sock := filepath.Join(data0, "fc-test.sock")
	if err := ioutil.WriteFile(sock, []byte(""), 0644); err != nil {
		t.Fatal(err)
	}
	fifo := filepath.Join(data0, "fc-metrics-test.fifo")
	if err := ioutil.WriteFile(fifo, []byte(""), 0644); err != nil {
		t.Fatal(err)
	}

	// override dirs and ExecCommand to a stub that returns static output
	SocketDirs = []string{data0, localtmp}
	MetricsDir = data0
	ExecCommand = func(name string, args ...string) *exec.Cmd {
		return exec.Command("/bin/echo", "-n", "iptables: simulated")
	}

	outPath := filepath.Join(tmp, "report.txt")
	if err := RunReconcile(outPath); err != nil {
		t.Fatalf("RunReconcile failed: %v", err)
	}

	// read report and validate contents
	b, err := ioutil.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	s := string(b)
	if !strings.Contains(s, "fc-test.sock") {
		t.Fatalf("report missing socket entry: %s", s)
	}
	if !strings.Contains(s, "fc-metrics-test.fifo") {
		t.Fatalf("report missing metrics entry: %s", s)
	}
	if !strings.Contains(s, "iptables: simulated") {
		t.Fatalf("report missing iptables simulated output: %s", s)
	}
}
