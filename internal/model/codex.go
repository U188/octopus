package model

import (
	"fmt"
	"strings"
)

type CodexHeaderProfile string

const (
	CodexHeaderProfileWindows CodexHeaderProfile = "windows"
	CodexHeaderProfileLocal   CodexHeaderProfile = "local"
)

func (p CodexHeaderProfile) Normalize() CodexHeaderProfile {
	value := CodexHeaderProfile(strings.ToLower(strings.TrimSpace(string(p))))
	if value == "" {
		return CodexHeaderProfileWindows
	}
	return value
}

func (p CodexHeaderProfile) Validate() error {
	switch p.Normalize() {
	case CodexHeaderProfileWindows, CodexHeaderProfileLocal:
		return nil
	default:
		return fmt.Errorf("unsupported codex header profile: %s", p)
	}
}
