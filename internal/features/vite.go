package features

import (
	"path/filepath"

	"lazycmd/internal/config"
)

type Vite struct{}

func (Vite) Name() string { return "vite" }

func (Vite) Detect(repoRoot string) (bool, string) {
	for _, ext := range []string{"js", "ts", "mjs", "cjs", "mts", "cts"} {
		name := "vite.config." + ext
		if fileExists(filepath.Join(repoRoot, name)) {
			return true, name
		}
	}
	return false, ""
}

// Commands runs vite via npx so it works regardless of package manager;
// picking the detected manager up from the npm feature isn't worth the
// coupling for three fixed commands.
func (Vite) Commands(repoRoot string, settings map[string]string) ([]config.ResolvedCommand, error) {
	return []config.ResolvedCommand{
		{ID: namespacedID("vite", "dev"), Name: namespacedName("vite", "dev"), Mode: config.ModeBackground, Run: "npx vite dev"},
		{ID: namespacedID("vite", "preview"), Name: namespacedName("vite", "preview"), Mode: config.ModeBackground, Run: "npx vite preview"},
		{ID: namespacedID("vite", "build"), Name: namespacedName("vite", "build"), Mode: config.ModePanel, Run: "npx vite build"},
	}, nil
}
