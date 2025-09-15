package host

import (
	"fmt"
	"os"
	"strings"
)

const eventsHost = "events.e2b.dev"

func AddEventsHostEntry(hyperloopIP string) error {
	entry := fmt.Sprintf("%s %s", hyperloopIP, eventsHost)
	content, err := os.ReadFile("/etc/hosts")
	if err != nil {
		return fmt.Errorf("failed to read /etc/hosts: %w", err)
	}

	// If the entry already exists, skip
	if strings.Contains(string(content), entry) {
		return nil
	}

	// Otherwise, just append to the end
	f, err := os.OpenFile("/etc/hosts", os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("failed to open /etc/hosts: %w", err)
	}
	defer f.Close()

	if _, err := f.WriteString("\n" + entry + "\n"); err != nil {
		return fmt.Errorf("failed to append to /etc/hosts: %w", err)
	}

	return nil
}
