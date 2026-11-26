package features

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"lazycmd/internal/config"
)

type Npm struct{}

func (Npm) Name() string { return "npm" }

func (Npm) Detect(repoRoot string) (bool, string) {
	if fileExists(filepath.Join(repoRoot, "package.json")) {
		return true, "package.json"
	}
	if len(projectDirs(repoRoot, "package.json")) > 0 {
		return true, "package.json files"
	}
	return false, ""
}

type packageJSON struct {
	Name       string            `json:"name"`
	Scripts    map[string]string `json:"scripts"`
	Workspaces json.RawMessage   `json:"workspaces"`
}

// workspacePatterns reads "workspaces" in either shape npm, yarn and
// bun accept: an array of globs, or {"packages": [...]}.
func (p packageJSON) workspacePatterns() []string {
	if len(p.Workspaces) == 0 {
		return nil
	}
	var list []string
	if json.Unmarshal(p.Workspaces, &list) == nil {
		return list
	}
	var obj struct {
		Packages []string `json:"packages"`
	}
	if json.Unmarshal(p.Workspaces, &obj) == nil {
		return obj.Packages
	}
	return nil
}

type workspace struct {
	name    string
	dir     string // relative to the repo root
	scripts map[string]string
}

// findWorkspaces expands the workspace globs to directories holding a
// package.json. Only reads files; never invokes a package manager.
func findWorkspaces(repoRoot string, patterns []string) []workspace {
	var out []workspace
	seen := map[string]bool{}
	for _, pat := range patterns {
		matches, err := filepath.Glob(filepath.Join(repoRoot, pat))
		if err != nil {
			continue
		}
		for _, dir := range matches {
			rel, err := filepath.Rel(repoRoot, dir)
			if err != nil || seen[rel] {
				continue
			}
			data, err := os.ReadFile(filepath.Join(dir, "package.json"))
			if err != nil {
				continue
			}
			var pkg packageJSON
			if json.Unmarshal(data, &pkg) != nil || pkg.Name == "" {
				continue
			}
			seen[rel] = true
			out = append(out, workspace{name: pkg.Name, dir: rel, scripts: pkg.Scripts})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

func (Npm) Commands(repoRoot string, settings map[string]string) ([]config.ResolvedCommand, error) {
	label, err := labelSetting(settings)
	if err != nil {
		return nil, fmt.Errorf("npm.%v", err)
	}
	dirs := npmProjects(repoRoot)
	if len(dirs) == 0 {
		return nil, nil
	}
	// One package at the root: the plain form. Several, or none at the
	// root: each command asks which package (project) to run in.
	if len(dirs) == 1 && dirs[0] == "." {
		return npmIn(repoRoot, repoRoot)
	}
	var projects []projectCommands
	for _, d := range dirs {
		if cmds, err := npmIn(filepath.Join(repoRoot, filepath.FromSlash(d)), repoRoot); err == nil && len(cmds) > 0 {
			projects = append(projects, projectCommands{d, cmds})
		}
	}
	return mergeProjects(projects, label), nil
}

// npmProjects lists the folders with a package.json that are projects of
// their own: a package that another one lists in its workspaces is run
// from there, through the workspace picker, not as a project.
func npmProjects(repoRoot string) []string {
	dirs := projectDirs(repoRoot, "package.json")
	member := map[string]bool{}
	for _, d := range dirs {
		abs := filepath.Join(repoRoot, filepath.FromSlash(d))
		data, err := os.ReadFile(filepath.Join(abs, "package.json"))
		if err != nil {
			continue
		}
		var pkg packageJSON
		if json.Unmarshal(data, &pkg) != nil {
			continue
		}
		for _, ws := range findWorkspaces(abs, pkg.workspacePatterns()) {
			member[path.Join(d, filepath.ToSlash(ws.dir))] = true
		}
	}
	var out []string
	for _, d := range dirs {
		if !member[d] {
			out = append(out, d)
		}
	}
	return out
}

// npmIn builds the commands for the package in repoRoot. top is the
// repo's root, where the search for a lockfile ends.
func npmIn(repoRoot, top string) ([]config.ResolvedCommand, error) {
	data, err := os.ReadFile(filepath.Join(repoRoot, "package.json"))
	if err != nil {
		return nil, err
	}
	var pkg packageJSON
	if err := json.Unmarshal(data, &pkg); err != nil {
		return nil, err
	}

	pm := packageManagerAt(repoRoot, top)
	workspaces := findWorkspaces(repoRoot, pkg.workspacePatterns())

	// With workspaces, every script found in the root or any workspace
	// is offered, and a picker chooses where it runs ("." is the root).
	scripts := map[string]bool{}
	for name := range pkg.Scripts {
		scripts[name] = true
	}
	var params []config.Param
	if len(workspaces) > 0 {
		opts := []string{"."}
		for _, ws := range workspaces {
			opts = append(opts, ws.name)
			for name := range ws.scripts {
				scripts[name] = true
			}
		}
		params = []config.Param{{Name: "workspace", Type: config.ParamSelect, Options: config.Opts(opts...), Default: "."}}
	}

	names := make([]string, 0, len(scripts))
	for name := range scripts {
		names = append(names, name)
	}
	sort.Strings(names)

	cmds := make([]config.ResolvedCommand, 0, len(names))
	for _, name := range names {
		if name == "" || strings.HasPrefix(name, "-") {
			// A name starting with - would be read as an option by the
			// package manager, not as a script; no real script needs one.
			continue
		}
		cmd := config.ResolvedCommand{
			ID:   namespacedID("npm", name),
			Name: namespacedName("npm", name),
			// The script name comes from package.json, so it is quoted:
			// a key like "test; curl x | sh" must stay one argument to
			// the package manager, not become shell code.
			Run: pm + " run " + shellQuote(name),
			Preview: func(map[string]string) string {
				return strings.Join(npmArgs(pm, name, ""), " ")
			},
		}
		if len(workspaces) > 0 {
			cmd.Params = params
			cmd.Run = npmWorkspaceRun(pm, name)
			cmd.Preview = func(v map[string]string) string {
				return strings.Join(npmArgs(pm, name, v["workspace"]), " ")
			}
		}
		cmds = append(cmds, cmd)
	}
	return cmds, nil
}

// npmArgs is the argv for running script in workspace ("" or "." is the root).
func npmArgs(pm, script, ws string) []string {
	if ws == "" || ws == "." {
		return []string{pm, "run", script}
	}
	switch pm {
	case "pnpm":
		return []string{"pnpm", "--filter", ws, "run", script}
	case "yarn":
		return []string{"yarn", "workspace", ws, "run", script}
	case "bun":
		return []string{"bun", "run", "--filter", ws, script}
	default:
		return []string{"npm", "run", script, "--workspace", ws}
	}
}

// npmWorkspaceRun is the shell body for a workspace-aware script: the
// root form when no workspace (or ".") is picked, else the manager's own
// per-workspace form. The workspace name arrives as $LC_WORKSPACE, never
// spliced into the text.
func npmWorkspaceRun(pm, script string) string {
	q := shellQuote(script)
	return "if [ -z \"$LC_WORKSPACE\" ] || [ \"$LC_WORKSPACE\" = . ]; then\n" +
		"  exec " + strings.Join(npmArgs(pm, q, "."), " ") + "\n" +
		"fi\n" +
		"exec " + strings.Join(npmArgs(pm, q, `"$LC_WORKSPACE"`), " ")
}

// shellQuote makes s a single shell word. Plain names are left as they
// are so the common case stays readable; anything else is wrapped in
// single quotes, inside which the shell interprets nothing.
func shellQuote(s string) string {
	plain := s != ""
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("_-.:/@+=,", c)) {
			plain = false
			break
		}
	}
	if plain {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// packageManager picks the manager by which lockfile is present, never
// by running anything.
func packageManager(dir string) string {
	switch {
	case fileExists(filepath.Join(dir, "pnpm-lock.yaml")):
		return "pnpm"
	case fileExists(filepath.Join(dir, "yarn.lock")):
		return "yarn"
	case fileExists(filepath.Join(dir, "bun.lockb")):
		return "bun"
	default:
		return ""
	}
}

// packageManagerAt looks for a lockfile in dir and then in each folder
// above it up to top (a workspace's lockfile sits at the root of the
// monorepo), and falls back to npm.
func packageManagerAt(dir, top string) string {
	for d := dir; ; d = filepath.Dir(d) {
		if pm := packageManager(d); pm != "" {
			return pm
		}
		if d == top || d == filepath.Dir(d) {
			return "npm"
		}
	}
}
