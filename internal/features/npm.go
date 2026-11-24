package features

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"

	"lazycast/internal/config"
)

type Npm struct{}

func (Npm) Name() string { return "npm" }

func (Npm) Detect(repoRoot string) (bool, string) {
	if !fileExists(filepath.Join(repoRoot, "package.json")) {
		return false, ""
	}
	return true, "package.json"
}

type packageJSON struct {
	Scripts map[string]string `json:"scripts"`
}

func (Npm) Commands(repoRoot string, settings map[string]string) ([]config.ResolvedCommand, error) {
	data, err := os.ReadFile(filepath.Join(repoRoot, "package.json"))
	if err != nil {
		return nil, err
	}
	var pkg packageJSON
	if err := json.Unmarshal(data, &pkg); err != nil {
		return nil, err
	}

	pm := packageManager(repoRoot)
	names := make([]string, 0, len(pkg.Scripts))
	for name := range pkg.Scripts {
		names = append(names, name)
	}
	sort.Strings(names)

	cmds := make([]config.ResolvedCommand, 0, len(names))
	for _, name := range names {
		cmds = append(cmds, config.ResolvedCommand{
			ID:   namespacedID("npm", name),
			Name: namespacedName("npm", name),
			Mode: config.ModePanel,
			Run:  pm + " run " + name,
		})
	}
	return cmds, nil
}

// packageManager picks the manager by which lockfile is present, never
// by running anything.
func packageManager(repoRoot string) string {
	switch {
	case fileExists(filepath.Join(repoRoot, "pnpm-lock.yaml")):
		return "pnpm"
	case fileExists(filepath.Join(repoRoot, "yarn.lock")):
		return "yarn"
	case fileExists(filepath.Join(repoRoot, "bun.lockb")):
		return "bun"
	default:
		return "npm"
	}
}
