package features

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"lazycmd/internal/config"
)

// standIn puts a program called name in a new folder that prints the
// folder it runs in and its arguments, and returns that folder for PATH.
func standIn(t *testing.T, names ...string) string {
	t.Helper()
	bin := t.TempDir()
	for _, name := range names {
		writeFile(t, filepath.Join(bin, name), "#!/bin/sh\nprintf '<%s>' \"$(basename \"$PWD\")\"\nfor a; do printf '[%s]' \"$a\"; done; echo\n")
		if err := os.Chmod(filepath.Join(bin, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return bin
}

// runIn runs a command's shell body from repo with the stand-ins on PATH.
func runIn(t *testing.T, c config.ResolvedCommand, repo, bin string, env map[string]string) (string, error) {
	t.Helper()
	cmd := exec.Command("sh", "-c", c.Run)
	cmd.Dir = repo
	cmd.Env = append([]string{"PATH=" + bin + ":/usr/bin:/bin"}, envList(env)...)
	out, err := cmd.CombinedOutput()
	if _, statErr := os.Stat(filepath.Join(repo, "pwned")); statErr == nil {
		t.Fatal("a folder name was run as shell code")
	}
	return strings.TrimSpace(string(out)), err
}

func param(c config.ResolvedCommand, name string) (config.Param, bool) {
	for _, p := range c.Params {
		if p.Name == name {
			return p, true
		}
	}
	return config.Param{}, false
}

// optionsOf runs a param's options_cmd the way the form does.
func optionsOf(t *testing.T, p config.Param, env map[string]string) config.OptionList {
	t.Helper()
	cmd := exec.Command("sh", "-c", p.OptionsCmd)
	cmd.Env = append([]string{"PATH=/usr/bin:/bin"}, envList(env)...)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("options_cmd: %v", err)
	}
	return config.ParseOptionLines(string(out))
}

func values(o config.OptionList) string { return strings.Join(o.Values(), ",") }

func composeMonorepo(t *testing.T) string {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "api", "compose.yaml"), "services:\n  web:\n    image: x\n  db:\n    image: y\n")
	writeFile(t, filepath.Join(root, "worker", "compose.yml"), "services:\n  jobs:\n    image: z\n    profiles: [nightly]\n")
	return root
}

func TestComposeSeveralFilesAskWhichOne(t *testing.T) {
	root := composeMonorepo(t)
	if ok, src := (Compose{}).Detect(root); !ok || src != "compose files" {
		t.Fatalf("detect = %v %q", ok, src)
	}
	cmds, err := Compose{}.Commands(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	by := mavenNames(cmds)
	up := by["compose: up"]

	project, ok := param(up, "project")
	if !ok || !project.Required || values(project.Options) != "api,worker" {
		t.Fatalf("project = %+v", project)
	}
	if project.Options[0].Display() != "api" || project.Options[1].Display() != "worker" {
		t.Errorf("labels = %q, %q: the folder's name", project.Options[0].Display(), project.Options[1].Display())
	}

	// The services depend on the file picked.
	services, _ := param(up, "services")
	if services.OptionsCmd == "" || len(services.DependsOn) != 1 || services.DependsOn[0] != "project" {
		t.Fatalf("services should be filled in for the project: %+v", services)
	}
	if got := values(optionsOf(t, services, map[string]string{"LC_PROJECT": "api"})); got != "db,web" {
		t.Errorf("api services = %q", got)
	}
	if got := values(optionsOf(t, services, map[string]string{"LC_PROJECT": "worker"})); got != "jobs" {
		t.Errorf("worker services = %q", got)
	}
	// Only worker has profiles: the others get an explicit "none".
	profile, _ := param(up, "profile")
	if got := optionsOf(t, profile, map[string]string{"LC_PROJECT": "api"}); len(got) != 1 || got[0].Value != "" {
		t.Errorf("api profile options = %+v", got)
	}
	if got := optionsOf(t, profile, map[string]string{"LC_PROJECT": "worker"}); values(got) != ",nightly" {
		t.Errorf("worker profile options = %+v", got)
	}
	// logs keeps its "all services" entry, an option with an empty value.
	svc, _ := param(by["compose: logs"], "service")
	if got := optionsOf(t, svc, map[string]string{"LC_PROJECT": "api"}); len(got) != 3 || got[0].Value != "" || got[0].Display() != "(all services)" {
		t.Errorf("logs service options = %+v", got)
	}
	if !by["compose: exec"].Terminal {
		t.Error("exec still needs the real terminal")
	}
	if got := up.Preview(map[string]string{"project": "api", "services": "web,db", "detach": "true"}); got != "cd api && docker compose up -d web db" {
		t.Errorf("preview = %q", got)
	}
}

func TestComposeSeveralFilesRunInTheirOwnFolder(t *testing.T) {
	root := composeMonorepo(t)
	writeFile(t, filepath.Join(root, "x$(touch pwned)", "compose.yaml"), "services:\n  s:\n    image: i\n")
	cmds, _ := Compose{}.Commands(root, nil)
	by := mavenNames(cmds)
	bin := standIn(t, "docker")

	if got, _ := runIn(t, by["compose: up"], root, bin, map[string]string{"LC_PROJECT": "api", "LC_SERVICES": "web,db", "LC_DETACH": "true"}); got != "<api>[compose][up][-d][web][db]" {
		t.Errorf("up in api = %s", got)
	}
	if got, _ := runIn(t, by["compose: down"], root, bin, map[string]string{"LC_PROJECT": "worker", "LC_VOLUMES": "true"}); got != "<worker>[compose][down][-v]" {
		t.Errorf("down in worker = %s", got)
	}
	if got, _ := runIn(t, by["compose: ps"], root, bin, map[string]string{"LC_PROJECT": "x$(touch pwned)"}); got != "<x$(touch pwned)>[compose][ps]" {
		t.Errorf("odd folder = %s", got)
	}
	if got, err := runIn(t, by["compose: ps"], root, bin, nil); err == nil || !strings.Contains(got, "no project picked") {
		t.Errorf("without a project: %q %v", got, err)
	}
}

func TestProjectLabels(t *testing.T) {
	root := t.TempDir()
	for _, d := range []string{"a/app", "b/app", "tools"} {
		writeFile(t, filepath.Join(root, d, "compose.yaml"), "services:\n  s:\n    image: i\n")
	}
	label := func(settings map[string]string) []string {
		cmds, err := Compose{}.Commands(root, settings)
		if err != nil {
			t.Fatal(err)
		}
		p, _ := param(mavenNames(cmds)["compose: ps"], "project")
		var out []string
		for _, o := range p.Options {
			out = append(out, o.Display())
		}
		return out
	}
	// "app" is both folders' name, so those two are told apart by path.
	if got := strings.Join(label(nil), ","); got != "a/app,b/app,tools" {
		t.Errorf("folder labels = %q", got)
	}
	if got := strings.Join(label(map[string]string{"label": "path"}), ","); got != "a/app,b/app,tools" {
		t.Errorf("path labels = %q", got)
	}

	root2 := t.TempDir()
	writeFile(t, filepath.Join(root2, "services", "billing", "compose.yaml"), "services:\n  s:\n    image: i\n")
	writeFile(t, filepath.Join(root2, "compose.yaml"), "services:\n  s:\n    image: i\n")
	for mode, want := range map[string]string{"folder": "(root),billing", "path": "(root),services/billing"} {
		cmds, _ := Compose{}.Commands(root2, map[string]string{"label": mode})
		p, _ := param(mavenNames(cmds)["compose: ps"], "project")
		var got []string
		for _, o := range p.Options {
			got = append(got, o.Display())
		}
		if strings.Join(got, ",") != want {
			t.Errorf("label %s = %v, want %s", mode, got, want)
		}
	}
	if _, err := (Compose{}).Commands(root, map[string]string{"label": "full"}); err == nil {
		t.Error("an unknown label setting should be an error")
	}
}

func TestComposeOneFileAtTheRootKeepsThePlainForm(t *testing.T) {
	cmds, _ := Compose{}.Commands(composeRepo(t, "compose.yaml"), nil)
	if _, ok := param(mavenNames(cmds)["compose: up"], "project"); ok {
		t.Error("a single compose file at the root shouldn't ask which")
	}
}

func TestNpmSeveralPackages(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "web", "package.json"), `{"name":"web","scripts":{"dev":"vite","build":"vite build"}}`)
	writeFile(t, filepath.Join(root, "web", "pnpm-lock.yaml"), "")
	writeFile(t, filepath.Join(root, "api", "package.json"), `{"name":"api","scripts":{"start":"node .","build":"tsc"}}`)
	// A monorepo with workspaces: its members aren't projects of their own.
	writeFile(t, filepath.Join(root, "mono", "package.json"), `{"name":"mono","workspaces":["pkgs/*"],"scripts":{"build":"x"}}`)
	writeFile(t, filepath.Join(root, "mono", "yarn.lock"), "")
	writeFile(t, filepath.Join(root, "mono", "pkgs", "lib", "package.json"), `{"name":"lib","scripts":{"test":"t"}}`)
	writeFile(t, filepath.Join(root, "node_modules", "dep", "package.json"), `{"name":"dep","scripts":{"x":"y"}}`)

	if ok, src := (Npm{}).Detect(root); !ok || src != "package.json files" {
		t.Fatalf("detect = %v %q", ok, src)
	}
	cmds, err := Npm{}.Commands(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	by := mavenNames(cmds)

	build, _ := param(by["npm: build"], "project")
	if values(build.Options) != "api,mono,web" {
		t.Errorf("build projects = %q: lib is a workspace of mono, node_modules is skipped", values(build.Options))
	}
	dev, _ := param(by["npm: dev"], "project")
	start, _ := param(by["npm: start"], "project")
	if values(dev.Options) != "web" || values(start.Options) != "api" {
		t.Errorf("dev %q start %q: only the projects that have the script", values(dev.Options), values(start.Options))
	}

	// mono has workspaces, the others don't: the picker depends on the project.
	ws, ok := param(by["npm: build"], "workspace")
	if !ok || ws.OptionsCmd == "" {
		t.Fatalf("workspace = %+v", ws)
	}
	if got := values(optionsOf(t, ws, map[string]string{"LC_PROJECT": "mono"})); got != ".,lib" {
		t.Errorf("mono workspaces = %q", got)
	}
	if got := optionsOf(t, ws, map[string]string{"LC_PROJECT": "web"}); len(got) != 1 || got[0].Value != "" {
		t.Errorf("web has no workspaces: %+v", got)
	}

	// Each runs with its own package manager, in its own folder.
	bin := standIn(t, "npm", "pnpm", "yarn")
	cases := []struct {
		cmd  string
		env  map[string]string
		want string
	}{
		{"npm: build", map[string]string{"LC_PROJECT": "web"}, "<web>[run][build]"},
		{"npm: build", map[string]string{"LC_PROJECT": "api"}, "<api>[run][build]"},
		{"npm: start", map[string]string{"LC_PROJECT": "api"}, "<api>[run][start]"},
		{"npm: build", map[string]string{"LC_PROJECT": "mono", "LC_WORKSPACE": "lib"}, "<mono>[workspace][lib][run][build]"},
	}
	for _, c := range cases {
		cmd := by[c.cmd]
		if got, err := runIn(t, cmd, root, bin, c.env); got != c.want {
			t.Errorf("%s %v = %q (%v), want %s", c.cmd, c.env, got, err, c.want)
		}
	}
	if got := by["npm: build"].Preview(map[string]string{"project": "web"}); got != "cd web && pnpm run build" {
		t.Errorf("preview = %q", got)
	}
}

func TestNpmLockfileAboveTheProjectIsUsed(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "pnpm-lock.yaml"), "")
	writeFile(t, filepath.Join(root, "apps", "a", "package.json"), `{"name":"a","scripts":{"go":"x"}}`)
	writeFile(t, filepath.Join(root, "apps", "b", "package.json"), `{"name":"b","scripts":{"go":"x"}}`)
	cmds, _ := Npm{}.Commands(root, nil)
	if got := by(cmds)["npm: go"].Preview(map[string]string{"project": "apps/a"}); got != "cd apps/a && pnpm run go" {
		t.Errorf("preview = %q: the monorepo's lockfile at the root decides", got)
	}
}

func by(cmds []config.ResolvedCommand) map[string]config.ResolvedCommand { return mavenNames(cmds) }

func TestViteSeveralProjects(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "app", "vite.config.ts"), "")
	writeFile(t, filepath.Join(root, "docs", "vite.config.js"), "")
	cmds, err := Vite{}.Commands(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	dev := mavenNames(cmds)["vite: dev"]
	project, _ := param(dev, "project")
	if values(project.Options) != "app,docs" {
		t.Fatalf("project = %+v", project)
	}
	if got, _ := runIn(t, dev, root, standIn(t, "npx"), map[string]string{"LC_PROJECT": "docs"}); got != "<docs>[vite][dev]" {
		t.Errorf("run = %q", got)
	}
}

func TestPythonSeveralProjects(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "uv.lock"), "") // one lockfile for the repo
	writeFile(t, filepath.Join(root, "libs", "core", "pyproject.toml"), "[project]\nname='core'\n[project.scripts]\ncore = 'c:m'\n[tool.pytest.ini_options]\naddopts='-q'\n")
	writeFile(t, filepath.Join(root, "tools", "gen", "pyproject.toml"), "[project]\nname='gen'\n[project.scripts]\ngen = 'g:m'\n[tool.ruff]\nline-length=100\n")
	if ok, src := (Python{}).Detect(root); !ok || src != "pyproject.toml files" {
		t.Fatalf("detect = %v %q", ok, src)
	}
	cmds, err := Python{}.Commands(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	by := mavenNames(cmds)
	pytest, _ := param(by["python: pytest"], "project")
	ruff, _ := param(by["python: ruff check"], "project")
	if values(pytest.Options) != "libs/core" || values(ruff.Options) != "tools/gen" {
		t.Errorf("pytest %q ruff %q: only where it's configured", values(pytest.Options), values(ruff.Options))
	}
	if got, _ := runIn(t, by["python: pytest"], root, standIn(t, "uv"), map[string]string{"LC_PROJECT": "libs/core", "LC_FILTER": "slow"}); got != "<core>[run][pytest][-k][slow]" {
		t.Errorf("pytest = %q", got)
	}
	if got := by["python: gen"].Preview(map[string]string{"project": "tools/gen"}); got != "cd tools/gen && uv run gen" {
		t.Errorf("preview = %q: the uv.lock at the root decides the runner", got)
	}
	// Two projects share a folder name: told apart by path.
	writeFile(t, filepath.Join(root, "x", "core", "pyproject.toml"), "[project]\nname='core2'\n[project.scripts]\ncore = 'c:m'\n")
	cmds, _ = Python{}.Commands(root, nil)
	p, _ := param(mavenNames(cmds)["python: core"], "project")
	if p.Options[0].Display() != "libs/core" || p.Options[1].Display() != "x/core" {
		t.Errorf("labels = %q, %q", p.Options[0].Display(), p.Options[1].Display())
	}
}

func TestWatchPathsCoverNestedProjectFiles(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "svc", "compose.yaml"), "services: {}\n")
	writeFile(t, filepath.Join(root, "web", "package.json"), "{}")
	writeFile(t, filepath.Join(root, "py", "pyproject.toml"), "")
	writeFile(t, filepath.Join(root, "ui", "vite.config.ts"), "")
	got := map[string]bool{}
	for _, p := range WatchPaths(root) {
		rel, _ := filepath.Rel(root, p)
		got[filepath.ToSlash(rel)] = true
	}
	for _, want := range []string{
		"svc/compose.yaml", "svc/compose.yml", "web/package.json", "web/pnpm-lock.yaml", "py/pyproject.toml", "py/uv.lock",
		"ui/vite.config.ts", "compose.yaml", "pom.xml",
	} {
		if !got[want] {
			t.Errorf("%s isn't watched", want)
		}
	}
}

func TestMergedCommandsKeepEachProjectsOwnParams(t *testing.T) {
	// A param only some projects have must not break the others.
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a", "compose.yaml"), "services:\n  s:\n    image: i\n    profiles: [p1]\n")
	writeFile(t, filepath.Join(root, "b", "compose.yaml"), "services:\n  t:\n    image: i\n")
	cmds, _ := Compose{}.Commands(root, nil)
	up := mavenNames(cmds)["compose: up"]
	bin := standIn(t, "docker")
	if got, _ := runIn(t, up, root, bin, map[string]string{"LC_PROJECT": "b", "LC_PROFILE": ""}); got != "<b>[compose][up]" {
		t.Errorf("up in b = %q", got)
	}
	if got, _ := runIn(t, up, root, bin, map[string]string{"LC_PROJECT": "a", "LC_PROFILE": "p1"}); got != "<a>[compose][--profile][p1][up]" {
		t.Errorf("up in a = %q", got)
	}
}
