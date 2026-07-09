package main

import (
	"encoding/json"
	"fmt"
	"io"
)

// renderJSON writes the report — or, for --recursive, the dependency-ordered
// chain — as indented JSON: the machine/AI mode, with no summarization.
func renderJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return fmt.Errorf("encode json: %w", err)
	}

	return nil
}
