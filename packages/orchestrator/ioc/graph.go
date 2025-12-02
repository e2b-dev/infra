package ioc

import (
	"fmt"
	"os"

	"go.uber.org/fx"
)

func newDebugGraphModule() fx.Option {
	fileName := os.Getenv("DUMP_GRAPH_DOT_FILE")

	return If("dot-graph",
		fileName != "",
		fx.Invoke(renderDotGraph(fileName)),
	).Build()
}

func renderDotGraph(fileName string) func(graph fx.DotGraph) error {
	return func(graph fx.DotGraph) error {
		if _, err := os.Stat(fileName); err == nil {
			if err := os.Remove(fileName); err != nil {
				return fmt.Errorf("failed to remove graph.dot file: %w", err)
			}
		}

		if err := os.WriteFile(fileName, []byte(graph), 0o664); err != nil {
			return fmt.Errorf("failed to write graph.dot file: %w", err)
		}

		return nil
	}
}
