package features

import (
	"fmt"
	"path/filepath"

	"lazycmd/internal/config"
)

type Vite struct{}

func (Vite) Name() string { return "vite" }

// viteConfigs are the file names that mark a Vite project.
var viteConfigs = []string{
	"vite.config.js", "vite.config.ts", "vite.config.mjs", "vite.config.cjs", "vite.config.mts", "vite.config.cts",
}

func viteConfigIn(dir string) string {
	for _, name := range viteConfigs {
		if fileExists(filepath.Join(dir, name)) {
			return name
		}
	}
	return ""
}

func (Vite) Detect(repoRoot string) (bool, string) {
	if name := viteConfigIn(repoRoot); name != "" {
		return true, name
	}
	if len(projectDirs(repoRoot, viteConfigs...)) > 0 {
		return true, "vite configs"
	}
	return false, ""
}

// Commands runs vite via npx so it works regardless of package manager;
// picking the detected manager up from the npm feature isn't worth the
// coupling for three fixed commands. With several Vite projects, each
// command asks which one to run in.
func (Vite) Commands(repoRoot string, settings map[string]string) ([]config.ResolvedCommand, error) {
	label, err := labelSetting(settings)
	if err != nil {
		return nil, fmt.Errorf("vite.%v", err)
	}
	dirs := projectDirs(repoRoot, viteConfigs...)
	if len(dirs) == 0 {
		return nil, nil
	}
	if len(dirs) == 1 && dirs[0] == "." {
		return viteCommands(), nil
	}
	var projects []projectCommands
	for _, d := range dirs {
		projects = append(projects, projectCommands{d, viteCommands()})
	}
	return mergeProjects(projects, label), nil
}

func viteCommands() []config.ResolvedCommand {
	return []config.ResolvedCommand{
		{ID: namespacedID("vite", "dev"), Name: namespacedName("vite", "dev"), Run: "npx vite dev",
			Preview: func(map[string]string) string { return "npx vite dev" }},
		{ID: namespacedID("vite", "preview"), Name: namespacedName("vite", "preview"), Run: "npx vite preview",
			Preview: func(map[string]string) string { return "npx vite preview" }},
		{ID: namespacedID("vite", "build"), Name: namespacedName("vite", "build"), Run: "npx vite build",
			Preview: func(map[string]string) string { return "npx vite build" }},
	}
}
