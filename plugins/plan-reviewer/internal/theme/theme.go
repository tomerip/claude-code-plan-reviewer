package theme

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Detect returns the user's Claude Code theme by reading
// ~/.claude/settings.local.json and ~/.claude/settings.json (local overrides user).
// Returns "dark" as a fallback.
func Detect() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "dark"
	}
	candidates := []string{
		filepath.Join(home, ".claude", "settings.local.json"),
		filepath.Join(home, ".claude", "settings.json"),
	}
	for _, p := range candidates {
		if t := readTheme(p); t != "" {
			return t
		}
	}
	return "dark"
}

func readTheme(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var s struct {
		Theme string `json:"theme"`
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return ""
	}
	return s.Theme
}
