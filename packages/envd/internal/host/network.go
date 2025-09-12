package host

import (
	"fmt"
	"os"
	"strings"
)

func AddEventsHostEntry(address string) error {
	hostsEntry := fmt.Sprintf("%s events.e2b.dev", address)
	// Read existing hosts file
	content, err := os.ReadFile("/etc/hosts")
	if err != nil {
		return fmt.Errorf("failed to read /etc/hosts: %w", err)
	}

	// Filter out any existing events.e2b.dev entries
	lines := strings.Split(string(content), "\n")
	filteredLines := make([]string, 0, len(lines))
	for _, line := range lines {
		if line == "" {
			continue
		}
		if !strings.Contains(line, "events.e2b.dev") {
			filteredLines = append(filteredLines, line)
		}
	}

	// Add the new entry
	filteredLines = append(filteredLines, hostsEntry)

	// Write back to file
	f, err := os.OpenFile("/etc/hosts", os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("failed to open /etc/hosts: %w", err)
	}
	defer f.Close()

	if _, err := f.WriteString(strings.Join(filteredLines, "\n") + "\n"); err != nil {
		return fmt.Errorf("failed to write to /etc/hosts: %w", err)
	}

	return nil
}
