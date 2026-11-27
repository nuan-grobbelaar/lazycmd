// Package features turns repo conventions (a pom.xml, package.json, a
// vite config, .git) into command specs in the same schema used by
// commands.yaml and sidecars. Detection only ever parses files - never
// shells out to mvn/npm/vite/git to discover what's there - so opening
// lazycmd in a repo never executes anything from that repo.
package features

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"lazycmd/internal/config"
	"lazycmd/internal/safefile"
)

type Feature interface {
	Name() string
	// Detect reports whether this feature applies to repoRoot, and if
	// so, the file that triggered it (for the group's display label).
	Detect(repoRoot string) (ok bool, sourceFile string)
	Commands(repoRoot string, settings map[string]string) ([]config.ResolvedCommand, error)
}

var All = []Feature{
	Maven{},
	Npm{},
	Vite{},
	Compose{},
	Python{},
	Git{},
}

var displayName = map[string]string{
	"maven":   "Maven",
	"npm":     "NPM",
	"vite":    "Vite",
	"compose": "Docker Compose",
	"python":  "Python",
	"git":     "git",
}

// Discover runs every enabled, detected feature and wraps its commands
// in a "Discovered" group. A feature that errors is skipped with a
// warning; it never aborts the others.
func Discover(repoRoot string, enabled map[string]bool, settings map[string]map[string]string, mavenGoals []config.MavenGoal) (groups []config.Group, warnings []string) {
	for _, f := range All {
		if m, ok := f.(Maven); ok {
			m.Goals = mavenGoals
			f = m
		}
		if !enabled[f.Name()] {
			continue
		}
		ok, sourceFile := f.Detect(repoRoot)
		if !ok {
			continue
		}

		cmds, err := f.Commands(repoRoot, settings[f.Name()])
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("%s: %v", f.Name(), err))
			continue
		}
		if len(cmds) == 0 {
			continue
		}

		title := fmt.Sprintf("Discovered — %s (%s)", displayName[f.Name()], sourceFile)
		groups = append(groups, config.Group{Title: title, Commands: cmds})
	}
	return groups, warnings
}

func namespacedName(feature, action string) string { return feature + ": " + action }
func namespacedID(feature, action string) string   { return feature + ":" + action }

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// WatchPaths lists the files the features read for repoRoot, so a change
// to one can rebuild the command list. In the root and in every folder
// below it that holds a project file (pom.xml, package.json,
// pyproject.toml, a compose file, a vite config), it lists all the names
// a feature looks for there, missing ones too: a file appearing is a
// change. It also lists the package.json of each npm workspace. The
// search walks folders, so callers should not ask for this on every tick.
func WatchPaths(repoRoot string) []string {
	markers := []string{"pom.xml", "package.json", "pyproject.toml"}
	markers = append(markers, composeFiles...)
	markers = append(markers, viteConfigs...)
	names := append([]string{"pnpm-lock.yaml", "yarn.lock", "bun.lockb", "uv.lock", "poetry.lock", "pdm.lock"}, markers...)

	dirs := map[string]bool{".": true}
	walkFor(repoRoot, func(n string) bool { return contains(markers, n) }, func(rel, _ string) bool {
		dirs[rel] = true
		return len(dirs) < maxScanned
	})
	var paths []string
	for dir := range dirs {
		for _, n := range names {
			paths = append(paths, filepath.Join(repoRoot, filepath.FromSlash(dir), n))
		}
	}
	for dir := range dirs {
		abs := filepath.Join(repoRoot, filepath.FromSlash(dir))
		if data, err := safefile.ReadFile(filepath.Join(abs, "package.json")); err == nil {
			var pkg packageJSON
			if json.Unmarshal(data, &pkg) == nil {
				for _, ws := range findWorkspaces(abs, pkg.workspacePatterns()) {
					paths = append(paths, filepath.Join(abs, ws.dir, "package.json"))
				}
			}
		}
	}
	return paths
}
