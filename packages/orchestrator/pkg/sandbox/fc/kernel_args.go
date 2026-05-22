//go:build linux

package fc

import (
	"fmt"
	"slices"
	"strings"
)

type KernelArgs map[string]string

func (ka KernelArgs) String() string {
	args := make([]string, 0, len(ka))
	for k, v := range ka {
		if v == "" {
			args = append(args, k)
		} else {
			args = append(args, fmt.Sprintf("%s=%s", k, v))
		}
	}
	slices.Sort(args) // optional: for consistent output

	return strings.Join(args, " ")
}
