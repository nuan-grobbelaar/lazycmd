package features

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	toml "github.com/pelletier/go-toml/v2"

	"lazycmd/internal/config"
)

// Python is the Python feature: it reads pyproject.toml (never runs
// Python, pip or any tool to find out what's there) and lists the
// project's scripts, tasks and the tools it configures.
type Python struct{}

func (Python) Name() string { return "python" }

func (Python) Detect(repoRoot string) (bool, string) {
	if fileExists(filepath.Join(repoRoot, "pyproject.toml")) {
		return true, "pyproject.toml"
	}
	if len(projectDirs(repoRoot, "pyproject.toml")) > 0 {
		return true, "pyproject.toml files"
	}
	return false, ""
}

// pythonRunners maps python.runner to the words put in front of a
// program. "plain" runs it in whatever environment is active. The setting
// ends up in a shell command, and config.yaml can come from a repo, so it
// has to be one of these.
var pythonRunners = map[string]string{
	"uv":     "uv run",
	"poetry": "poetry run",
	"pdm":    "pdm run",
	"plain":  "",
}

// pythonRunner picks the runner by which lockfile is present in dir or
// above it up to top (a uv workspace has one uv.lock at its root), never
// by running anything.
func pythonRunner(dir, top string) string {
	for d := dir; ; d = filepath.Dir(d) {
		switch {
		case fileExists(filepath.Join(d, "uv.lock")):
			return "uv"
		case fileExists(filepath.Join(d, "poetry.lock")):
			return "poetry"
		case fileExists(filepath.Join(d, "pdm.lock")):
			return "pdm"
		}
		if d == top || d == filepath.Dir(d) {
			return "plain"
		}
	}
}

func (Python) Commands(repoRoot string, settings map[string]string) ([]config.ResolvedCommand, error) {
	label, err := labelSetting(settings)
	if err != nil {
		return nil, fmt.Errorf("python.%v", err)
	}
	if r := settings["runner"]; r != "" {
		if _, ok := pythonRunners[r]; !ok {
			return nil, fmt.Errorf("python.runner must be one of uv, poetry, pdm, plain, not %q", r)
		}
	}
	dirs := projectDirs(repoRoot, "pyproject.toml")
	if len(dirs) == 0 {
		return nil, nil
	}
	// One project at the root: the plain form. Several, or none at the
	// root: each command asks which project to run in.
	if len(dirs) == 1 && dirs[0] == "." {
		return pythonIn(repoRoot, repoRoot, settings)
	}
	var projects []projectCommands
	for _, d := range dirs {
		if cmds, err := pythonIn(filepath.Join(repoRoot, filepath.FromSlash(d)), repoRoot, settings); err == nil && len(cmds) > 0 {
			projects = append(projects, projectCommands{d, cmds})
		}
	}
	return mergeProjects(projects, label), nil
}

// pythonIn builds the commands for the project in repoRoot; top is the
// repo's root, where the search for a lockfile ends.
func pythonIn(repoRoot, top string, settings map[string]string) ([]config.ResolvedCommand, error) {
	runner := settings["runner"]
	if runner == "" {
		runner = pythonRunner(repoRoot, top)
	}
	prefix := pythonRunners[runner]

	data, err := os.ReadFile(filepath.Join(repoRoot, "pyproject.toml"))
	if err != nil {
		return nil, err
	}
	var doc map[string]any
	if err := toml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("pyproject.toml: %v", err)
	}

	// run builds the shell body for a program (an executable, or with
	// module set a tool that also runs as python -m <module>) and the
	// arguments already in "$@".
	run := func(prog string, module bool) string {
		cmd := prog
		switch {
		case prefix != "":
			cmd = prefix + " " + prog
		case module:
			cmd = "python -m " + prog
		}
		return "exec " + cmd + ` "$@"`
	}

	var cmds []config.ResolvedCommand
	have := map[string]bool{}
	add := func(c config.ResolvedCommand) {
		if !have[c.ID] {
			have[c.ID] = true
			cmds = append(cmds, c)
		}
	}
	// Names from the file are quoted (a key like "x; curl y | sh" must
	// stay one argument), and one starting with - would be read as an
	// option, not a name, so it's skipped.
	named := func(names []string, build func(name string) config.ResolvedCommand) {
		sort.Strings(names)
		for _, n := range names {
			if n == "" || strings.HasPrefix(n, "-") || strings.ContainsAny(n, "\n\r") {
				continue
			}
			add(build(n))
		}
	}
	simple := func(name, body string) config.ResolvedCommand {
		return config.ResolvedCommand{
			ID: namespacedID("python", name), Name: namespacedName("python", name), Run: body,
			Preview: func(map[string]string) string { return previewOf(body) },
		}
	}

	// Tasks defined in the file, run through the tool that defines them.
	named(keys(table(doc, "tool", "pdm", "scripts")), func(n string) config.ResolvedCommand {
		return simple(n, "exec pdm run "+shellQuote(n))
	})
	named(keys(table(doc, "tool", "poe", "tasks")), func(n string) config.ResolvedCommand {
		cmd := "poe"
		if prefix != "" {
			cmd = prefix + " poe"
		}
		return simple(n, "exec "+cmd+" "+shellQuote(n))
	})
	// Console scripts: installed executables, so they run in the
	// project's environment.
	console := append(keys(table(doc, "project", "scripts")), keys(table(doc, "tool", "poetry", "scripts"))...)
	named(console, func(n string) config.ResolvedCommand {
		cmd := shellQuote(n)
		if prefix != "" {
			cmd = prefix + " " + cmd
		}
		return simple(n, "exec "+cmd)
	})

	if table(doc, "tool", "pytest", "ini_options") != nil || hasDependency(doc, "pytest") {
		c := config.ResolvedCommand{
			ID: namespacedID("python", "pytest"), Name: namespacedName("python", "pytest"),
			Params: []config.Param{
				{Name: "path", Type: config.ParamString},
				{Name: "filter", Type: config.ParamString},
			},
			Run: "set --\n[ -n \"$LC_FILTER\" ] && set -- \"$@\" -k \"$LC_FILTER\"\n[ -n \"$LC_PATH\" ] && set -- \"$@\" -- \"$LC_PATH\"\n" + run("pytest", true),
			Preview: func(v map[string]string) string {
				args := strings.Fields(pythonCmd(prefix, "pytest", true))
				if v["filter"] != "" {
					args = append(args, "-k", shellQuote(v["filter"]))
				}
				if v["path"] != "" {
					args = append(args, "--", shellQuote(v["path"]))
				}
				return strings.Join(args, " ")
			},
		}
		add(c)
	}
	if table(doc, "tool", "ruff") != nil {
		for _, sub := range []string{"check", "format"} {
			sub := sub
			add(config.ResolvedCommand{
				ID: namespacedID("python", "ruff "+sub), Name: namespacedName("python", "ruff "+sub),
				Run:     "set -- " + sub + "\n" + run("ruff", true),
				Preview: func(map[string]string) string { return pythonCmd(prefix, "ruff", true) + " " + sub },
			})
		}
	}
	if table(doc, "tool", "mypy") != nil {
		add(config.ResolvedCommand{
			ID: namespacedID("python", "mypy"), Name: namespacedName("python", "mypy"),
			Run:     run("mypy", true),
			Preview: func(map[string]string) string { return pythonCmd(prefix, "mypy", true) },
		})
	}
	return cmds, nil
}

// pythonCmd is the command line for prog with the runner's prefix, as
// the script runs it.
func pythonCmd(prefix, prog string, module bool) string {
	switch {
	case prefix != "":
		return prefix + " " + prog
	case module:
		return "python -m " + prog
	}
	return prog
}

// previewOf turns a one-line "exec ..." body into the command it runs.
func previewOf(body string) string {
	return strings.TrimPrefix(body, "exec ")
}

// table returns the nested TOML table at path, or nil when any step is
// missing or not a table.
func table(doc map[string]any, path ...string) map[string]any {
	cur := doc
	for _, p := range path {
		next, ok := cur[p].(map[string]any)
		if !ok {
			return nil
		}
		cur = next
	}
	return cur
}

func keys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// hasDependency reports whether pyproject.toml lists name as a
// dependency anywhere: project dependencies, optional ones, dependency
// groups, or Poetry's and PDM's own tables.
func hasDependency(doc map[string]any, name string) bool {
	match := func(req string) bool {
		req = strings.ToLower(strings.TrimSpace(req))
		end := strings.IndexAny(req, " [<>=!~;(@")
		if end >= 0 {
			req = req[:end]
		}
		return strings.ReplaceAll(strings.ReplaceAll(req, "_", "-"), ".", "-") == name
	}
	anyMatch := func(v any) bool {
		list, _ := v.([]any)
		for _, item := range list {
			if s, ok := item.(string); ok && match(s) {
				return true
			}
		}
		return false
	}
	anyIn := func(m map[string]any) bool { // a table of lists
		for _, v := range m {
			if anyMatch(v) {
				return true
			}
		}
		return false
	}
	hasKey := func(m map[string]any) bool { // a table keyed by package
		for k := range m {
			if match(k) {
				return true
			}
		}
		return false
	}

	if anyMatch(table(doc, "project")["dependencies"]) ||
		anyIn(table(doc, "project", "optional-dependencies")) ||
		anyIn(table(doc, "dependency-groups")) ||
		anyIn(table(doc, "tool", "pdm", "dev-dependencies")) ||
		hasKey(table(doc, "tool", "poetry", "dependencies")) ||
		hasKey(table(doc, "tool", "poetry", "dev-dependencies")) {
		return true
	}
	for _, g := range table(doc, "tool", "poetry", "group") {
		if grp, ok := g.(map[string]any); ok && hasKey(table(grp, "dependencies")) {
			return true
		}
	}
	return false
}
