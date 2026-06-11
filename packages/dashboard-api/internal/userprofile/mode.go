package userprofile

import (
	"fmt"
	"strings"
)

type Mode string

const (
	ModeSupabase Mode = "supabase"
	ModeOry      Mode = "ory"
)

func ParseMode(value string) (Mode, error) {
	switch Mode(strings.TrimSpace(value)) {
	case ModeSupabase:
		return ModeSupabase, nil
	case ModeOry:
		return ModeOry, nil
	default:
		return "", fmt.Errorf("invalid user profile provider %q (want one of %q, %q)", value, ModeSupabase, ModeOry)
	}
}

func (m Mode) RequiresOry() bool {
	return m == ModeOry
}
