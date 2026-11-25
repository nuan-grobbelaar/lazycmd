// Package features turns repo conventions (a pom.xml, package.json, a
// vite config, .git) into command specs in the same schema used by
// commands.yaml and sidecars. Detection only ever parses files - never
// shells out to mvn/npm/vite/git to discover what's there - so opening
// lazycmd in a repo never executes anything from that repo.
package features

import (
	"fmt"
	"os"

	"lazycmd/internal/config"
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
	Git{},
}

var displayName = map[string]string{
	"maven": "Maven",
	"npm":   "NPM",
	"vite":  "Vite",
	"git":   "git",
}

// Discover runs every enabled, detected feature and wraps its commands
// in a "Discovered" group. A feature that errors is skipped with a
// warning; it never aborts the others.
func Discover(repoRoot string, enabled map[string]bool, settings map[string]map[string]string) (groups []config.Group, warnings []string) {
	for _, f := range All {
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

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
