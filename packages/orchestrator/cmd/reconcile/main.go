package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/reconcile"
)

func main() {
	out := flag.String("out", "", "output report path (default /tmp/reconcile-report-<ts>.txt)")
	flag.Parse()

	if err := reconcile.RunReconcile(*out); err != nil {
		fmt.Fprintf(os.Stderr, "reconcile error: %v\n", err)
		os.Exit(2)
	}
}
