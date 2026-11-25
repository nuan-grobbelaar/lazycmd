package features

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lazycmd/internal/config"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func names(t *testing.T, f Feature, repoRoot string, settings map[string]string) []string {
	t.Helper()
	cmds, err := f.Commands(repoRoot, settings)
	if err != nil {
		t.Fatalf("Commands: %v", err)
	}
	var out []string
	for _, c := range cmds {
		out = append(out, c.Name)
	}
	return out
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

func TestMavenDetectAndLifecycle(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "pom.xml"), `<project></project>`)

	ok, source := Maven{}.Detect(root)
	if !ok || source != "pom.xml" {
		t.Fatalf("Detect = %v, %q", ok, source)
	}

	ns := names(t, Maven{}, root, nil)
	for _, phase := range []string{"maven: test", "maven: package", "maven: deploy"} {
		if !contains(ns, phase) {
			t.Errorf("missing lifecycle phase %q in %v", phase, ns)
		}
	}
}

func TestMavenModulesProfilesAndLinters(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "pom.xml"), `
<project>
  <modules>
    <module>core</module>
    <module>api</module>
  </modules>
  <profiles>
    <profile><id>ci</id></profile>
    <profile><id>release</id></profile>
  </profiles>
  <build>
    <plugins>
      <plugin><artifactId>maven-checkstyle-plugin</artifactId></plugin>
      <plugin><artifactId>spotless-maven-plugin</artifactId></plugin>
      <plugin><artifactId>some-other-plugin</artifactId></plugin>
    </plugins>
  </build>
</project>`)

	cmds, err := Maven{}.Commands(root, map[string]string{"default_profile": "ci"})
	if err != nil {
		t.Fatal(err)
	}

	var testCmdFound bool
	for _, c := range cmds {
		if c.Name != "maven: test" {
			continue
		}
		testCmdFound = true
		var hasModule, hasProfile bool
		for _, p := range c.Params {
			if p.Name == "module" {
				hasModule = true
				if len(p.Options) != 2 {
					t.Errorf("module options = %v, want 2", p.Options)
				}
			}
			if p.Name == "profile" {
				hasProfile = true
				if p.Default != "ci" {
					t.Errorf("profile default = %q, want ci", p.Default)
				}
			}
		}
		if !hasModule || !hasProfile {
			t.Errorf("maven: test params = %+v, missing module/profile", c.Params)
		}
	}
	if !testCmdFound {
		t.Fatal("maven: test command not found")
	}

	ns := names(t, Maven{}, root, nil)
	if !contains(ns, "maven: checkstyle:check") {
		t.Errorf("missing checkstyle linter command in %v", ns)
	}
	if !contains(ns, "maven: spotless:check") {
		t.Errorf("missing spotless linter command in %v", ns)
	}
}

func TestMavenCacheInvalidatesOnMtimeChange(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "pom.xml")
	writeFile(t, path, `<project><modules><module>a</module></modules></project>`)

	pom1, err := parsePomCached(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(pom1.Modules) != 1 {
		t.Fatalf("modules = %v", pom1.Modules)
	}

	// Bump mtime forward so a content change is observed even on
	// filesystems with coarse mtime resolution.
	future := time.Now().Add(time.Hour)
	writeFile(t, path, `<project><modules><module>a</module><module>b</module></modules></project>`)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatal(err)
	}

	pom2, err := parsePomCached(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(pom2.Modules) != 2 {
		t.Errorf("modules after change = %v, want 2 (cache didn't invalidate)", pom2.Modules)
	}
}

func TestNpmDetectAndScripts(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "package.json"), `{"scripts": {"build": "x", "dev": "y", "lint": "z"}}`)
	writeFile(t, filepath.Join(root, "pnpm-lock.yaml"), "")

	ok, source := Npm{}.Detect(root)
	if !ok || source != "package.json" {
		t.Fatalf("Detect = %v, %q", ok, source)
	}

	cmds, err := Npm{}.Commands(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(cmds) != 3 {
		t.Fatalf("len(cmds) = %d, want 3", len(cmds))
	}
	for _, c := range cmds {
		if c.Name == "npm: build" && c.Run != "pnpm run build" {
			t.Errorf("run = %q, want pnpm run build (lockfile should pick pnpm)", c.Run)
		}
	}
}

func TestNpmNotDetectedWithoutPackageJSON(t *testing.T) {
	if ok, _ := (Npm{}).Detect(t.TempDir()); ok {
		t.Error("Detect = true with no package.json")
	}
}

func TestViteDetectVariousExtensions(t *testing.T) {
	for _, ext := range []string{"js", "ts", "mts"} {
		root := t.TempDir()
		writeFile(t, filepath.Join(root, "vite.config."+ext), "")
		ok, source := Vite{}.Detect(root)
		if !ok || source != "vite.config."+ext {
			t.Errorf("ext %s: Detect = %v, %q", ext, ok, source)
		}
	}

	got := names(t, Vite{}, t.TempDir(), nil)
	for _, want := range []string{"vite: dev", "vite: preview", "vite: build"} {
		if !contains(got, want) {
			t.Errorf("missing %q in %v", want, got)
		}
	}
}

func TestGitDetect(t *testing.T) {
	root := t.TempDir()
	if ok, _ := (Git{}).Detect(root); ok {
		t.Error("Detect = true with no .git")
	}
	writeFile(t, filepath.Join(root, ".git", "HEAD"), "ref: refs/heads/main\n")
	ok, source := Git{}.Detect(root)
	if !ok || source != ".git" {
		t.Fatalf("Detect = %v, %q", ok, source)
	}
}

func TestGitCommands(t *testing.T) {
	ns := names(t, Git{}, t.TempDir(), nil)
	for _, want := range []string{"git: fetch --prune", "git: pull", "git: push", "git: stash", "git: stash pop", "git: worktree list"} {
		if !contains(ns, want) {
			t.Errorf("missing %q in %v", want, ns)
		}
	}
}

func TestMavenDetectWithoutPomXML(t *testing.T) {
	if ok, _ := (Maven{}).Detect(t.TempDir()); ok {
		t.Error("Detect = true with no pom.xml")
	}
}

func TestViteDetectWithoutConfig(t *testing.T) {
	if ok, _ := (Vite{}).Detect(t.TempDir()); ok {
		t.Error("Detect = true with no vite.config.*")
	}
}

func TestPackageManagerPicksByLockfile(t *testing.T) {
	cases := []struct {
		lockfile string
		want     string
	}{
		{"yarn.lock", "yarn"},
		{"bun.lockb", "bun"},
		{"package-lock.json", "npm"}, // not specially detected; npm is the default
	}
	for _, c := range cases {
		root := t.TempDir()
		if c.lockfile != "package-lock.json" {
			writeFile(t, filepath.Join(root, c.lockfile), "")
		}
		if got := packageManager(root); got != c.want {
			t.Errorf("lockfile %s: packageManager = %q, want %q", c.lockfile, got, c.want)
		}
	}
}

func TestDiscoverSkipsDisabledAndUndetected(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "pom.xml"), `<project></project>`)
	// npm is enabled but there's no package.json here, so it must not appear.

	groups, warnings := Discover(root, map[string]bool{"maven": true, "npm": true}, nil)
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if len(groups) != 1 {
		t.Fatalf("groups = %+v, want exactly 1 (maven only)", groups)
	}
	if groups[0].Title != "Discovered — Maven (pom.xml)" {
		t.Errorf("title = %q", groups[0].Title)
	}
}

// runWithStub runs a command's Run body under sh with a stub binary
// named bin on PATH that prints its argv, returning that output.
func runWithStub(t *testing.T, bin, body string, env map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, bin), "#!/bin/sh\necho \"$@\"\n")
	if err := os.Chmod(filepath.Join(dir, bin), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", "-c", body)
	cmd.Env = append([]string{"PATH=" + dir + ":/usr/bin:/bin"}, envList(env)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("sh: %v\n%s", err, out)
	}
	return strings.TrimSpace(string(out))
}

func envList(m map[string]string) []string {
	var out []string
	for k, v := range m {
		out = append(out, k+"="+v)
	}
	return out
}

func TestMavenAlsoMakeParamAndRun(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "pom.xml"), `<project><modules><module>core</module><module>api</module></modules></project>`)
	cmds, err := Maven{}.Commands(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	var test config.ResolvedCommand
	for _, c := range cmds {
		if c.Name == "maven: test" {
			test = c
		}
	}
	var gotAM bool
	for _, p := range test.Params {
		if p.Name == "also_make" && p.Type == config.ParamBool {
			gotAM = true
		}
	}
	if !gotAM {
		t.Fatalf("params = %+v, want an also_make bool", test.Params)
	}

	cases := []struct {
		env  map[string]string
		want string
	}{
		{map[string]string{"LC_MODULE": "api", "LC_ALSO_MAKE": "true", "LC_PROFILE": "local"}, "-pl api -am -Plocal test"},
		{map[string]string{"LC_MODULE": "api", "LC_ALSO_MAKE": "false"}, "-pl api test"},
		{map[string]string{"LC_ALSO_MAKE": "true"}, "test"}, // no module: -am alone is meaningless
	}
	for _, c := range cases {
		if got := runWithStub(t, "mvn", test.Run, c.env); got != c.want {
			t.Errorf("env %v: mvn argv = %q, want %q", c.env, got, c.want)
		}
	}

	prev := test.Preview(map[string]string{"module": "api", "also_make": "true", "profile": "local"})
	if prev != "mvn test -pl api -am -Plocal" {
		t.Errorf("preview = %q", prev)
	}
}

func TestMavenNoModulesNoAlsoMake(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "pom.xml"), `<project></project>`)
	cmds, _ := Maven{}.Commands(root, nil)
	for _, c := range cmds {
		for _, p := range c.Params {
			if p.Name == "also_make" {
				t.Fatalf("%s offers also_make without modules", c.Name)
			}
		}
	}
}

func TestNpmWorkspacesPicker(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "package.json"), `{"name":"root","workspaces":["packages/*"],"scripts":{"lint":"x"}}`)
	writeFile(t, filepath.Join(root, "pnpm-lock.yaml"), "")
	writeFile(t, filepath.Join(root, "packages/web/package.json"), `{"name":"@app/web","scripts":{"dev":"vite","lint":"y"}}`)
	writeFile(t, filepath.Join(root, "packages/api/package.json"), `{"name":"@app/api","scripts":{"test":"jest"}}`)
	writeFile(t, filepath.Join(root, "packages/notapkg/readme.md"), "")

	cmds, err := Npm{}.Commands(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	byName := map[string]config.ResolvedCommand{}
	for _, c := range cmds {
		names = append(names, c.Name)
		byName[c.Name] = c
	}
	for _, want := range []string{"npm: dev", "npm: lint", "npm: test"} {
		if !contains(names, want) {
			t.Errorf("missing %q in %v", want, names)
		}
	}
	lint := byName["npm: lint"]
	if len(lint.Params) != 1 || lint.Params[0].Name != "workspace" {
		t.Fatalf("params = %+v", lint.Params)
	}
	opts := lint.Params[0].Options.Values()
	if len(opts) != 3 || opts[0] != "." || opts[1] != "@app/api" || opts[2] != "@app/web" {
		t.Errorf("options = %v, want [. @app/api @app/web]", opts)
	}

	if got := runWithStub(t, "pnpm", lint.Run, map[string]string{"LC_WORKSPACE": "."}); got != "run lint" {
		t.Errorf("root run argv = %q", got)
	}
	if got := runWithStub(t, "pnpm", lint.Run, map[string]string{"LC_WORKSPACE": "@app/web"}); got != "--filter @app/web run lint" {
		t.Errorf("workspace run argv = %q", got)
	}
	if got := lint.Preview(map[string]string{"workspace": "@app/web"}); got != "pnpm --filter @app/web run lint" {
		t.Errorf("preview = %q", got)
	}
}

func TestNpmWorkspacesObjectForm(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "package.json"), `{"workspaces":{"packages":["libs/*"]},"scripts":{"build":"x"}}`)
	writeFile(t, filepath.Join(root, "libs/a/package.json"), `{"name":"a"}`)
	cmds, _ := Npm{}.Commands(root, nil)
	if len(cmds) != 1 || len(cmds[0].Params) != 1 {
		t.Fatalf("cmds = %+v", cmds)
	}
	if got := cmds[0].Preview(map[string]string{"workspace": "a"}); got != "npm run build --workspace a" {
		t.Errorf("preview = %q", got)
	}
}

// A package.json script name is repo-controlled; it must reach the
// package manager as one argument, never as shell code.
func TestNpmScriptNameIsNotShellCode(t *testing.T) {
	evil := `x; echo INJECTED; $(echo SUB) 'q' #`
	root := t.TempDir()
	marker := filepath.Join(t.TempDir(), "pwned")
	pkg := `{"scripts": {` +
		`"` + strings.ReplaceAll(evil, `"`, `\"`) + `": "a",` +
		`"touch ` + marker + `": "b",` +
		`"--prefix=/tmp": "c",` +
		`"build": "d"}}`
	writeFile(t, filepath.Join(root, "package.json"), pkg)
	cmds, err := Npm{}.Commands(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]config.ResolvedCommand{}
	for _, c := range cmds {
		byName[c.Name] = c
	}
	if _, ok := byName["npm: --prefix=/tmp"]; ok {
		t.Error("a script name starting with - should be skipped")
	}
	if got := byName["npm: build"].Run; got != "npm run build" {
		t.Errorf("plain name should stay readable: %q", got)
	}
	if got := runWithStub(t, "npm", byName["npm: "+evil].Run, nil); got != "run "+evil {
		t.Errorf("npm argv = %q, want the name as one argument", got)
	}
	runWithStub(t, "npm", byName["npm: touch "+marker].Run, nil)
	if _, err := os.Stat(marker); err == nil {
		t.Error("script name was executed by the shell")
	}
}

func TestNpmWorkspaceScriptNameIsNotShellCode(t *testing.T) {
	evil := `t;echo INJECTED`
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "package.json"), `{"workspaces":["packages/*"],"scripts":{"`+evil+`":"x"}}`)
	writeFile(t, filepath.Join(root, "packages/web/package.json"), `{"name":"web"}`)
	cmds, _ := Npm{}.Commands(root, nil)
	if len(cmds) != 1 {
		t.Fatalf("cmds = %+v", cmds)
	}
	if got := runWithStub(t, "npm", cmds[0].Run, map[string]string{"LC_WORKSPACE": "."}); got != "run "+evil {
		t.Errorf("root argv = %q", got)
	}
	if got := runWithStub(t, "npm", cmds[0].Run, map[string]string{"LC_WORKSPACE": "web"}); got != "run "+evil+" --workspace web" {
		t.Errorf("workspace argv = %q", got)
	}
}

func findCmd(t *testing.T, cmds []config.ResolvedCommand, name string) config.ResolvedCommand {
	t.Helper()
	for _, c := range cmds {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("command %q not found", name)
	return config.ResolvedCommand{}
}

func paramByName(c config.ResolvedCommand, name string) *config.Param {
	for i := range c.Params {
		if c.Params[i].Name == name {
			return &c.Params[i]
		}
	}
	return nil
}

const springPlugin = `<build><plugins><plugin><groupId>org.springframework.boot</groupId><artifactId>spring-boot-maven-plugin</artifactId></plugin></plugins></build>`

func TestMavenSpringBootRunInModule(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "pom.xml"), `<project>
  <modules><module>core</module><module>services</module></modules>
  <profiles><profile><id>local</id></profile></profiles>
</project>`)
	writeFile(t, filepath.Join(root, "core", "pom.xml"), `<project></project>`)
	writeFile(t, filepath.Join(root, "services", "pom.xml"), `<project><modules><module>web</module><module>worker</module></modules></project>`)
	writeFile(t, filepath.Join(root, "services", "web", "pom.xml"), `<project>`+springPlugin+`</project>`)
	writeFile(t, filepath.Join(root, "services", "worker", "pom.xml"), `<project></project>`)

	cmds, err := Maven{}.Commands(root, map[string]string{"default_profile": "local"})
	if err != nil {
		t.Fatal(err)
	}
	run := findCmd(t, cmds, "maven: spring-boot:run")

	mod := paramByName(run, "module")
	if mod == nil || len(mod.Options) != 1 || mod.Options[0].Value != "services/web" || !mod.Required || mod.Default != "services/web" {
		t.Fatalf("module param = %+v, want only services/web, required, preselected", mod)
	}
	if p := paramByName(run, "profile"); p == nil || p.Default != "local" {
		t.Errorf("profile param = %+v, want Maven profiles with default_profile", p)
	}
	if paramByName(run, "also_make") != nil {
		t.Error("spring-boot:run offers also_make; -am would start upstream modules too")
	}
	if paramByName(run, "spring_profiles") == nil {
		t.Error("spring-boot:run has no spring_profiles param")
	}

	got := runWithStub(t, "mvn", run.Run, map[string]string{
		"LC_MODULE": "services/web", "LC_PROFILE": "local", "LC_SPRING_PROFILES": "dev,debug",
	})
	if want := "-pl services/web -Plocal -Dspring-boot.run.profiles=dev,debug spring-boot:run"; got != want {
		t.Errorf("mvn argv = %q, want %q", got, want)
	}
	got = runWithStub(t, "mvn", run.Run, map[string]string{"LC_MODULE": "services/web"})
	if want := "-pl services/web spring-boot:run"; got != want {
		t.Errorf("mvn argv without profiles = %q, want %q", got, want)
	}

	prev := run.Preview(map[string]string{"module": "services/web", "spring_profiles": "dev"})
	if prev != "mvn spring-boot:run -pl services/web -Dspring-boot.run.profiles=dev" {
		t.Errorf("preview = %q", prev)
	}
}

func TestMavenRunGoalDeclaredAtRoot(t *testing.T) {
	// Single-module app: no module picker, runs at the root.
	single := t.TempDir()
	writeFile(t, filepath.Join(single, "pom.xml"), `<project>`+springPlugin+`</project>`)
	cmds, _ := Maven{}.Commands(single, nil)
	run := findCmd(t, cmds, "maven: spring-boot:run")
	if paramByName(run, "module") != nil {
		t.Errorf("single-module params = %+v, want no module picker", run.Params)
	}
	if got := runWithStub(t, "mvn", run.Run, nil); got != "spring-boot:run" {
		t.Errorf("mvn argv = %q", got)
	}

	// Parent declares it: children inherit it, so every module is offered.
	parent := t.TempDir()
	writeFile(t, filepath.Join(parent, "pom.xml"), `<project><modules><module>a</module><module>b</module></modules>`+springPlugin+`</project>`)
	writeFile(t, filepath.Join(parent, "a", "pom.xml"), `<project></project>`)
	writeFile(t, filepath.Join(parent, "b", "pom.xml"), `<project></project>`)
	cmds, _ = Maven{}.Commands(parent, nil)
	mod := paramByName(findCmd(t, cmds, "maven: spring-boot:run"), "module")
	if mod == nil || strings.Join(mod.Options.Values(), ",") != "a,b" {
		t.Errorf("module param = %+v, want a,b", mod)
	}
}

func TestMavenRunGoalsOnlyWhenDeclared(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "pom.xml"), `<project>
  <modules><module>app</module></modules>
  <build><pluginManagement><plugins><plugin><artifactId>spring-boot-maven-plugin</artifactId></plugin></plugins></pluginManagement></build>
</project>`)
	writeFile(t, filepath.Join(root, "app", "pom.xml"), `<project><build><plugins>
  <plugin><artifactId>quarkus-maven-plugin</artifactId></plugin>
  <plugin><artifactId>jetty-maven-plugin</artifactId></plugin>
</plugins></build></project>`)

	ns := names(t, Maven{}, root, nil)
	if contains(ns, "maven: spring-boot:run") {
		t.Error("pluginManagement alone offered spring-boot:run")
	}
	for _, want := range []string{"maven: quarkus:dev", "maven: jetty:run"} {
		if !contains(ns, want) {
			t.Errorf("%s missing from %v", want, ns)
		}
	}

	cmds, _ := Maven{}.Commands(root, nil)
	dev := findCmd(t, cmds, "maven: quarkus:dev")
	got := runWithStub(t, "mvn", dev.Run, map[string]string{"LC_MODULE": "app", "LC_QUARKUS_PROFILE": "test"})
	if got != "-pl app -Dquarkus.profile=test quarkus:dev" {
		t.Errorf("quarkus argv = %q", got)
	}
	if jetty := findCmd(t, cmds, "maven: jetty:run"); len(jetty.Params) != 1 {
		t.Errorf("jetty params = %+v, want just module", jetty.Params)
	}

	// No plugin anywhere: no run goals at all.
	plain := t.TempDir()
	writeFile(t, filepath.Join(plain, "pom.xml"), `<project><modules><module>x</module></modules></project>`)
	writeFile(t, filepath.Join(plain, "x", "pom.xml"), `<project></project>`)
	for _, n := range names(t, Maven{}, plain, nil) {
		if strings.Contains(n, ":run") || strings.Contains(n, ":dev") {
			t.Errorf("unexpected run goal %q", n)
		}
	}
}

func TestMavenModulesStayInsideRepo(t *testing.T) {
	outside := t.TempDir()
	writeFile(t, filepath.Join(outside, "pom.xml"), `<project>`+springPlugin+`</project>`)

	root := t.TempDir()
	rel, err := filepath.Rel(root, outside)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "pom.xml"), `<project><modules>
  <module>`+filepath.ToSlash(rel)+`</module>
  <module>`+outside+`</module>
  <module>missing</module>
  <module>./ok/</module>
  <module>ok</module>
</modules></project>`)
	writeFile(t, filepath.Join(root, "ok", "pom.xml"), `<project>`+springPlugin+`</project>`)
	writeFile(t, filepath.Join(root, "ok", "pom.xml"), `<project><modules><module>../ok</module></modules>`+springPlugin+`</project>`)

	cmds, err := Maven{}.Commands(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	mod := paramByName(findCmd(t, cmds, "maven: spring-boot:run"), "module")
	if mod == nil || strings.Join(mod.Options.Values(), ",") != "ok" {
		t.Errorf("module options = %+v, want just ok (outside, missing and duplicate entries dropped)", mod)
	}
}

func TestMavenModuleCycleTerminates(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "pom.xml"), `<project><modules><module>a</module></modules></project>`)
	writeFile(t, filepath.Join(root, "a", "pom.xml"), `<project><modules><module>../a</module><module>b</module></modules></project>`)
	writeFile(t, filepath.Join(root, "a", "b", "pom.xml"), `<project><modules><module>..</module></modules>`+springPlugin+`</project>`)
	cmds, _ := Maven{}.Commands(root, nil)
	mod := paramByName(findCmd(t, cmds, "maven: spring-boot:run"), "module")
	if mod == nil || strings.Join(mod.Options.Values(), ",") != "a/b" {
		t.Errorf("module options = %+v, want a/b", mod)
	}
}
