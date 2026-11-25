package features

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"lazycmd/internal/config"
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
	data, err := os.ReadFile(filepath.Join(repoRoot, "package.json"))
	if err != nil {
		return nil, err
	}
	var pkg packageJSON
	if err := json.Unmarshal(data, &pkg); err != nil {
		return nil, err
	}

	pm := packageManager(repoRoot)
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
		params = []config.Param{{Name: "workspace", Type: config.ParamSelect, Options: opts, Default: "."}}
	}

	names := make([]string, 0, len(scripts))
	for name := range scripts {
		names = append(names, name)
	}
	sort.Strings(names)

	cmds := make([]config.ResolvedCommand, 0, len(names))
	for _, name := range names {
		cmd := config.ResolvedCommand{
			ID:   namespacedID("npm", name),
			Name: namespacedName("npm", name),
			Mode: config.ModePanel,
			Run:  pm + " run " + name,
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
	sub := npmArgs(pm, script, "$LC_WORKSPACE")
	return "if [ -z \"$LC_WORKSPACE\" ] || [ \"$LC_WORKSPACE\" = . ]; then\n" +
		"  exec " + strings.Join(npmArgs(pm, script, "."), " ") + "\n" +
		"fi\n" +
		"exec " + strings.Join(quoteWS(sub), " ")
}

// quoteWS double-quotes the $LC_WORKSPACE argument.
func quoteWS(args []string) []string {
	out := make([]string, len(args))
	for i, a := range args {
		if a == "$LC_WORKSPACE" {
			a = `"$LC_WORKSPACE"`
		}
		out[i] = a
	}
	return out
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
