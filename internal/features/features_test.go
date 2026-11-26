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

	withConfig := t.TempDir()
	writeFile(t, filepath.Join(withConfig, "vite.config.ts"), "")
	got := names(t, Vite{}, withConfig, nil)
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
		if got := packageManagerAt(root, root); got != c.want {
			t.Errorf("lockfile %s: packageManager = %q, want %q", c.lockfile, got, c.want)
		}
	}
}

func TestDiscoverSkipsDisabledAndUndetected(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "pom.xml"), `<project></project>`)
	// npm is enabled but there's no package.json here, so it must not appear.

	groups, warnings := Discover(root, map[string]bool{"maven": true, "npm": true}, nil, nil)
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

func mavenNames(cmds []config.ResolvedCommand) map[string]config.ResolvedCommand {
	out := map[string]config.ResolvedCommand{}
	for _, c := range cmds {
		out[c.Name] = c
	}
	return out
}

func TestMavenGoalsWrapperPluginIsDetected(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "pom.xml"), `<project><build><plugins>
		<plugin><artifactId>acme-boot-maven-plugin</artifactId></plugin>
	</plugins></build></project>`)

	cmds, _ := Maven{}.Commands(root, nil)
	if _, ok := mavenNames(cmds)["maven: spring-boot:run"]; ok {
		t.Fatal("the wrapper isn't known yet, spring-boot:run shouldn't show")
	}

	m := Maven{Goals: []config.MavenGoal{{Goal: "spring-boot:run", Plugin: "acme-boot-maven-plugin"}}}
	cmds, err := m.Commands(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	run, ok := mavenNames(cmds)["maven: spring-boot:run"]
	if !ok {
		t.Fatalf("wrapper plugin not detected: %v", mavenNames(cmds))
	}
	// It is still the built-in run goal: it keeps the spring profiles field.
	var hasProfiles bool
	for _, p := range run.Params {
		hasProfiles = hasProfiles || p.Name == "spring_profiles"
	}
	if !hasProfiles || !strings.HasSuffix(run.Run, "exec mvn \"$@\" spring-boot:run") {
		t.Errorf("run = %q, params = %+v", run.Run, run.Params)
	}
	for _, g := range pluginGoals {
		if g.goal == "spring-boot:run" && len(g.artifactIDs) != 1 {
			t.Errorf("the built-in table was changed: %v", g.artifactIDs)
		}
	}
}

func TestMavenGoalsWithoutPluginAlwaysShowAndPickModules(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "pom.xml"), `<project><modules><module>api</module><module>web</module></modules></project>`)
	writeFile(t, filepath.Join(root, "api", "pom.xml"), `<project/>`)
	writeFile(t, filepath.Join(root, "web", "pom.xml"), `<project/>`)

	m := Maven{Goals: []config.MavenGoal{{Goal: "spring-boot:run"}}}
	cmds, _ := m.Commands(root, nil)
	run, ok := mavenNames(cmds)["maven: spring-boot:run"]
	if !ok {
		t.Fatal("a goal with no plugin should always show")
	}
	var mod config.Param
	for _, p := range run.Params {
		if p.Name == "module" {
			mod = p
		}
	}
	if got := strings.Join(mod.Options.Values(), ","); got != "api,web" || !mod.Required {
		t.Errorf("module = %+v, want a required pick of every module", mod)
	}
}

func TestMavenGoalsCustomGoalAndDuplicates(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "pom.xml"), `<project/>`)
	yes := true
	m := Maven{Goals: []config.MavenGoal{
		{Goal: "clean install"},
		{Goal: "test"}, // already a lifecycle command
		{Goal: "acme:serve", Run: &yes, ProfileParam: "acme_env", ProfileProp: "acme.env"},
	}}
	cmds, _ := m.Commands(root, nil)
	byName := mavenNames(cmds)
	if c, ok := byName["maven: clean install"]; !ok || !strings.HasSuffix(c.Run, "exec mvn \"$@\" clean install") {
		t.Errorf("clean install = %+v", c)
	}
	n := 0
	for _, c := range cmds {
		if c.Name == "maven: test" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("maven: test listed %d times", n)
	}
	serve, ok := byName["maven: acme:serve"]
	if !ok || !strings.Contains(serve.Run, `-Dacme.env=$LC_ACME_ENV`) {
		t.Errorf("acme:serve = %+v", serve)
	}
	if got := serve.Preview(map[string]string{"acme_env": "dev"}); got != "mvn acme:serve -Dacme.env=dev" {
		t.Errorf("preview = %q", got)
	}
}

// config.yaml -> EnabledFeatures -> Discover, the way main wires it.
func TestMavenGoalsFromConfigFileReachDiscover(t *testing.T) {
	repo, cfg := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(repo, "pom.xml"), `<project><build><plugins>
		<plugin><artifactId>acme-boot-maven-plugin</artifactId></plugin></plugins></build></project>`)
	writeFile(t, filepath.Join(cfg, "config.yaml"), "maven:\n  goals:\n    - { goal: spring-boot:run, plugin: acme-boot-maven-plugin }\n")

	enabled, settings, goals, warnings := config.EnabledFeatures(t.TempDir(), cfg)
	groups, w := Discover(repo, enabled, settings, goals)
	if len(warnings)+len(w) != 0 {
		t.Fatalf("warnings: %v %v", warnings, w)
	}
	var found bool
	for _, g := range groups {
		for _, c := range g.Commands {
			found = found || c.Name == "maven: spring-boot:run"
		}
	}
	if !found {
		t.Fatalf("spring-boot:run missing: %+v", groups)
	}
}

const composeYAML = `
services:
  web:
    image: nginx
  db:
    image: postgres
  worker:
    image: app
    profiles: [jobs, batch]
  "bad name":
    image: x
`

func composeRepo(t *testing.T, name string) string {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, name), composeYAML)
	return root
}

func TestComposeDetectedByAnyComposeFileName(t *testing.T) {
	for _, name := range composeFiles {
		ok, src := Compose{}.Detect(composeRepo(t, name))
		if !ok || src != name {
			t.Errorf("%s: detect = %v %q", name, ok, src)
		}
	}
	if ok, _ := (Compose{}).Detect(t.TempDir()); ok {
		t.Error("an empty repo has no compose file")
	}
}

func TestComposeCommands(t *testing.T) {
	cmds, err := Compose{}.Commands(composeRepo(t, "compose.yaml"), nil)
	if err != nil {
		t.Fatal(err)
	}
	by := mavenNames(cmds)
	for _, name := range []string{"up", "down", "ps", "logs", "build", "restart", "pull", "exec"} {
		if _, ok := by["compose: "+name]; !ok {
			t.Errorf("compose: %s missing; have %v", name, by)
		}
	}
	up := by["compose: up"]
	var services, profile config.Param
	for _, p := range up.Params {
		switch p.Name {
		case "services":
			services = p
		case "profile":
			profile = p
		}
	}
	if got := strings.Join(services.Options.Values(), ","); got != "db,web,worker" { // "bad name" is skipped
		t.Errorf("services = %q", got)
	}
	if got := strings.Join(profile.Options.Values(), ","); got != ",batch,jobs" { // a leading "none"
		t.Errorf("profiles = %q", got)
	}
	if !by["compose: exec"].Terminal {
		t.Error("exec is interactive: it needs the real terminal")
	}
	if got := up.Preview(map[string]string{"services": "web,db", "detach": "true", "build": "true", "profile": "jobs"}); got != "docker compose --profile jobs up -d --build web db" {
		t.Errorf("preview = %q", got)
	}
	if got := by["compose: logs"].Preview(map[string]string{"follow": "true"}); got != "docker compose logs -f" {
		t.Errorf("logs preview = %q", got)
	}
	if got := by["compose: exec"].Preview(map[string]string{"service": "web", "command": "ls -la /app"}); got != "docker compose exec web sh -c 'ls -la /app'" {
		t.Errorf("exec preview = %q", got)
	}
}

// The shell bodies are run for real, with a stand-in for docker that
// prints its arguments, so what reaches the CLI is checked, not just the
// preview text.
func TestComposeRunScriptsPassTheRightArguments(t *testing.T) {
	cmds, _ := Compose{}.Commands(composeRepo(t, "compose.yaml"), map[string]string{"command": "podman compose"})
	by := mavenNames(cmds)

	bin := t.TempDir()
	writeFile(t, filepath.Join(bin, "podman"), "#!/bin/sh\nfor a; do printf '[%s]' \"$a\"; done; echo\n")
	if err := os.Chmod(filepath.Join(bin, "podman"), 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(name string, env map[string]string) string {
		cmd := exec.Command("sh", "-c", by[name].Run)
		cmd.Env = append([]string{"PATH=" + bin + ":/usr/bin:/bin"}, envList(env)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v\n%s", name, err, out)
		}
		return strings.TrimSpace(string(out))
	}

	if got := run("compose: up", map[string]string{"LC_SERVICES": "web,db", "LC_DETACH": "true", "LC_BUILD": "false", "LC_PROFILE": "jobs"}); got != "[compose][--profile][jobs][up][-d][web][db]" {
		t.Errorf("up = %s", got)
	}
	if got := run("compose: up", map[string]string{"LC_SERVICES": "*,$(id)"}); got != "[compose][up][*][$(id)]" {
		t.Errorf("a service value must stay literal, got %s", got) // no globbing, no expansion
	}
	if got := run("compose: down", map[string]string{"LC_VOLUMES": "true"}); got != "[compose][down][-v]" {
		t.Errorf("down = %s", got)
	}
	if got := run("compose: logs", map[string]string{"LC_SERVICE": "web", "LC_FOLLOW": "true"}); got != "[compose][logs][-f][web]" {
		t.Errorf("logs = %s", got)
	}
	if got := run("compose: logs", nil); got != "[compose][logs]" {
		t.Errorf("logs of everything = %s", got)
	}
	if got := run("compose: exec", map[string]string{"LC_SERVICE": "db", "LC_COMMAND": "psql -c 'select 1'; echo hi"}); got != "[compose][exec][db][sh][-c][psql -c 'select 1'; echo hi]" {
		t.Errorf("exec = %s", got)
	}
	if got := run("compose: exec", map[string]string{"LC_SERVICE": "db"}); got != "[compose][exec][db][sh][-c][sh]" {
		t.Errorf("exec default = %s", got)
	}
}

func TestComposeCommandSettingIsAllowlisted(t *testing.T) {
	root := composeRepo(t, "compose.yaml")
	for _, bad := range []string{"docker; rm -rf ~", "docker compose && id", "sh"} {
		if _, err := (Compose{}).Commands(root, map[string]string{"command": bad}); err == nil {
			t.Errorf("compose.command %q accepted", bad)
		}
	}
	cmds, err := Compose{}.Commands(root, map[string]string{"command": "docker-compose"})
	if err != nil || !strings.HasSuffix(mavenNames(cmds)["compose: ps"].Run, `exec docker-compose "$@"`) {
		t.Errorf("docker-compose: %v", err)
	}
}

const pyprojectTOML = `
[project]
name = "demo"
dependencies = ["requests>=2", "Pytest-Cov"]
[project.optional-dependencies]
test = ["pytest[extras] >= 7"]
[project.scripts]
demo = "demo.cli:main"
"-bad" = "x:y"
[tool.poetry.scripts]
serve = "demo.web:run"
[tool.pdm.scripts]
lint = "ruff check ."
[tool.poe.tasks]
docs = "mkdocs build"
"a; touch pwned" = "echo no"
[tool.ruff]
line-length = 100
[tool.mypy]
strict = true
`

func pyRepo(t *testing.T, toml string, lock string) string {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "pyproject.toml"), toml)
	if lock != "" {
		writeFile(t, filepath.Join(root, lock), "")
	}
	return root
}

func TestPythonDetectAndCommands(t *testing.T) {
	root := pyRepo(t, pyprojectTOML, "uv.lock")
	if ok, src := (Python{}).Detect(root); !ok || src != "pyproject.toml" {
		t.Fatalf("detect = %v %q", ok, src)
	}
	cmds, err := Python{}.Commands(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	by := mavenNames(cmds)
	for _, name := range []string{"demo", "serve", "lint", "docs", "a; touch pwned", "pytest", "ruff check", "ruff format", "mypy"} {
		if _, ok := by["python: "+name]; !ok {
			t.Errorf("python: %s missing; have %v", name, by)
		}
	}
	if _, ok := by["python: -bad"]; ok {
		t.Error("a name starting with - would be read as an option")
	}
	want := map[string]string{
		"python: demo":        "exec uv run demo",
		"python: serve":       "exec uv run serve",
		"python: lint":        "exec pdm run lint", // only pdm knows pdm's scripts
		"python: docs":        "exec uv run poe docs",
		"python: ruff format": "set -- format\nexec uv run ruff \"$@\"",
		"python: mypy":        `exec uv run mypy "$@"`,
	}
	for name, run := range want {
		if by[name].Run != run {
			t.Errorf("%s: run = %q, want %q", name, by[name].Run, run)
		}
	}
	if got := by["python: pytest"].Preview(map[string]string{"filter": "slow and not db", "path": "tests/unit"}); got != "uv run pytest -k 'slow and not db' -- tests/unit" {
		t.Errorf("pytest preview = %q", got)
	}
}

func TestPythonRunnerFromLockfileAndSetting(t *testing.T) {
	cases := []struct{ lock, setting, want string }{
		{"uv.lock", "", "exec uv run demo"},
		{"poetry.lock", "", "exec poetry run demo"},
		{"pdm.lock", "", "exec pdm run demo"},
		{"", "", "exec demo"},
		{"uv.lock", "plain", "exec demo"},
		{"", "poetry", "exec poetry run demo"},
	}
	for _, c := range cases {
		root := pyRepo(t, "[project.scripts]\ndemo = \"d:m\"\n", c.lock)
		cmds, err := Python{}.Commands(root, map[string]string{"runner": c.setting})
		if err != nil || mavenNames(cmds)["python: demo"].Run != c.want {
			t.Errorf("lock %q setting %q: %q, %v; want %q", c.lock, c.setting, mavenNames(cmds)["python: demo"].Run, err, c.want)
		}
	}
	for _, bad := range []string{"uv; id", "pip", "uv run"} {
		if _, err := (Python{}).Commands(pyRepo(t, "[project]\n", ""), map[string]string{"runner": bad}); err == nil {
			t.Errorf("python.runner %q accepted", bad)
		}
	}
}

func TestPythonPytestOnlyWhenConfiguredOrDependency(t *testing.T) {
	has := func(toml string) bool {
		cmds, err := Python{}.Commands(pyRepo(t, toml, ""), nil)
		if err != nil {
			t.Fatal(err)
		}
		_, ok := mavenNames(cmds)["python: pytest"]
		return ok
	}
	for name, toml := range map[string]string{
		"ini_options":      "[tool.pytest.ini_options]\naddopts = \"-q\"\n",
		"dependency":       "[project]\ndependencies = [\"pytest>=8\"]\n",
		"dependency group": "[dependency-groups]\ndev = [\"pytest\", {include-group = \"x\"}]\n",
		"poetry":           "[tool.poetry.group.dev.dependencies]\npytest = \"^8\"\n",
	} {
		if !has(toml) {
			t.Errorf("%s: pytest not offered", name)
		}
	}
	// pytest-cov alone is not pytest.
	if has("[project]\ndependencies = [\"pytest-cov\", \"requests\"]\n") || has("[project]\nname = \"x\"\n") {
		t.Error("pytest offered without being configured or depended on")
	}
}

// The shell bodies run for real against stand-ins that print their
// arguments, so what reaches the tools is checked.
func TestPythonRunScriptsPassTheRightArguments(t *testing.T) {
	root := pyRepo(t, pyprojectTOML, "uv.lock")
	cmds, _ := Python{}.Commands(root, nil)
	by := mavenNames(cmds)

	bin := t.TempDir()
	writeFile(t, filepath.Join(bin, "uv"), "#!/bin/sh\nfor a; do printf '[%s]' \"$a\"; done; echo\n")
	if err := os.Chmod(filepath.Join(bin, "uv"), 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(name string, env map[string]string) string {
		cmd := exec.Command("sh", "-c", by[name].Run)
		cmd.Dir = t.TempDir() // a file the script creates would land here
		cmd.Env = append([]string{"PATH=" + bin + ":/usr/bin:/bin"}, envList(env)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v\n%s", name, err, out)
		}
		if _, err := os.Stat(filepath.Join(cmd.Dir, "pwned")); err == nil {
			t.Fatalf("%s: a task name was run as shell code", name)
		}
		return strings.TrimSpace(string(out))
	}
	if got := run("python: pytest", map[string]string{"LC_FILTER": "a b", "LC_PATH": "-x/tests"}); got != "[run][pytest][-k][a b][--][-x/tests]" {
		t.Errorf("pytest = %s", got)
	}
	if got := run("python: pytest", nil); got != "[run][pytest]" {
		t.Errorf("bare pytest = %s", got)
	}
	if got := run("python: docs", nil); got != "[run][poe][docs]" {
		t.Errorf("poe = %s", got)
	}
	if got := run("python: a; touch pwned", nil); got != "[run][poe][a; touch pwned]" {
		t.Errorf("quoted name = %s", got)
	}
	if got := run("python: ruff check", nil); got != "[run][ruff][check]" {
		t.Errorf("ruff = %s", got)
	}
}

func TestPythonBrokenTomlIsAnError(t *testing.T) {
	if _, err := (Python{}).Commands(pyRepo(t, "[project\nname=", ""), nil); err == nil {
		t.Error("a pyproject.toml that doesn't parse should be reported")
	}
}

// The demo folders in examples/ are documentation; keep them loading.
func TestFeatureExamplesLoad(t *testing.T) {
	dir := filepath.Join("..", "..", "examples", "features")

	cmds, err := Compose{}.Commands(filepath.Join(dir, "compose-demo"), nil)
	if err != nil {
		t.Fatal(err)
	}
	up := mavenNames(cmds)["compose: up"]
	var profile bool
	for _, p := range up.Params {
		profile = profile || p.Name == "profile"
	}
	if len(cmds) != 8 || !profile {
		t.Errorf("compose demo: %d commands, profile pick %v", len(cmds), profile)
	}

	cmds, err = Maven{}.Commands(filepath.Join(dir, "maven-monorepo"), nil)
	if err != nil {
		t.Fatal(err)
	}
	by := mavenNames(cmds)
	if got := strings.Join(targetOptions(by["maven: test"]), ","); got != "services/billing::,services/billing::api,services/billing::web,services/orders::" {
		t.Errorf("maven demo targets = %q", got)
	}
	if got := strings.Join(targetOptions(by["maven: spring-boot:run"]), ","); got != "services/billing::api" {
		t.Errorf("maven demo run targets = %q", got)
	}
	if got := strings.Join(targetOptions(by["maven: spotless:check"]), ","); got != "services/orders::" {
		t.Errorf("maven demo spotless targets = %q", got)
	}

	mono := filepath.Join(dir, "monorepo-demo")
	for feature, c := range map[string]struct {
		f    Feature
		cmd  string
		want string
	}{
		"npm":     {Npm{}, "npm: build", "web"},
		"vite":    {Vite{}, "vite: dev", "web"},
		"python":  {Python{}, "python: pytest", "api"},
		"compose": {Compose{}, "compose: up", "api,worker"},
	} {
		cmds, err := c.f.Commands(mono, nil)
		if err != nil {
			t.Fatalf("%s: %v", feature, err)
		}
		p, _ := param(mavenNames(cmds)[c.cmd], "project")
		if values(p.Options) != c.want {
			t.Errorf("monorepo demo %s: projects %q, want %q", c.cmd, values(p.Options), c.want)
		}
	}

	cmds, err = Python{}.Commands(filepath.Join(dir, "python-demo"), nil)
	if err != nil {
		t.Fatal(err)
	}
	by = mavenNames(cmds)
	for _, name := range []string{"demo", "docs", "pytest", "ruff check", "ruff format", "mypy"} {
		if _, ok := by["python: "+name]; !ok {
			t.Errorf("python demo: %s missing; have %v", name, by)
		}
	}
}

func writePom(t *testing.T, root, dir, body string) {
	t.Helper()
	writeFile(t, filepath.Join(root, dir, "pom.xml"), "<project>"+body+"</project>")
}

func targetOptions(c config.ResolvedCommand) []string {
	for _, p := range c.Params {
		if p.Name == "target" {
			return p.Options.Values()
		}
	}
	return nil
}

// A monorepo of independent Maven projects, with no pom at the root.
func monorepo(t *testing.T) string {
	root := t.TempDir()
	writePom(t, root, "services/billing", `<modules><module>api</module><module>web</module></modules>
		<profiles><profile><id>local</id></profile></profiles>`)
	writePom(t, root, "services/billing/api", `<build><plugins><plugin><artifactId>spring-boot-maven-plugin</artifactId></plugin></plugins></build>`)
	writePom(t, root, "services/billing/web", ``)
	writePom(t, root, "services/orders", `<build><plugins><plugin><artifactId>spotless-maven-plugin</artifactId></plugin></plugins></build>`)
	// Never searched.
	writePom(t, root, "node_modules/dep", ``)
	writePom(t, root, "services/orders/target/classes", ``)
	writePom(t, root, ".hidden/x", ``)
	writePom(t, root, "services/src/fixture", ``)
	return root
}

func TestMavenFindsNestedProjectsWithoutRootPom(t *testing.T) {
	root := monorepo(t)
	if ok, src := (Maven{}).Detect(root); !ok || src != "nested pom.xml files" {
		t.Fatalf("detect = %v %q", ok, src)
	}
	cmds, err := Maven{}.Commands(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	by := mavenNames(cmds)

	// billing's modules belong to billing; nothing in the skipped folders is a project.
	want := "services/billing::,services/billing::api,services/billing::web,services/orders::"
	if got := strings.Join(targetOptions(by["maven: test"]), ","); got != want {
		t.Errorf("test targets = %q, want %q", got, want)
	}
	// Plugin goals only where their plugin is declared.
	if got := strings.Join(targetOptions(by["maven: spotless:check"]), ","); got != "services/orders::" {
		t.Errorf("spotless targets = %q", got)
	}
	run, ok := by["maven: spring-boot:run"]
	if !ok || strings.Join(targetOptions(run), ",") != "services/billing::api" {
		t.Errorf("spring-boot:run = %v %v", ok, targetOptions(run))
	}
	// The label shows project and module; the value carries both.
	for _, p := range by["maven: test"].Params {
		if p.Name == "target" && p.Options[1].Display() != "services/billing › api" {
			t.Errorf("label = %q", p.Options[1].Display())
		}
	}
	if got := by["maven: test"].Preview(map[string]string{"target": "services/billing::api", "also_make": "true", "profile": "local"}); got != "mvn -f services/billing/pom.xml -pl api -am -Plocal test" {
		t.Errorf("preview = %q", got)
	}
	if got := run.Preview(map[string]string{"target": "services/billing::api", "also_make": "true", "spring_profiles": "dev"}); got != "mvn -f services/billing/pom.xml -pl api -Dspring-boot.run.profiles=dev spring-boot:run" {
		t.Errorf("run preview = %q (no -am for a run goal)", got)
	}
}

func TestMavenProjectScriptsRunWithTheRightArguments(t *testing.T) {
	root := monorepo(t)
	// A folder named like shell code must stay a plain argument.
	writePom(t, root, "x$(touch pwned)", ``)
	cmds, _ := Maven{}.Commands(root, nil)
	by := mavenNames(cmds)

	bin := t.TempDir()
	writeFile(t, filepath.Join(bin, "mvn"), "#!/bin/sh\nfor a; do printf '[%s]' \"$a\"; done; echo\n")
	if err := os.Chmod(filepath.Join(bin, "mvn"), 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(name string, env map[string]string) (string, error) {
		cmd := exec.Command("sh", "-c", by[name].Run)
		cmd.Dir = t.TempDir()
		cmd.Env = append([]string{"PATH=" + bin + ":/usr/bin:/bin"}, envList(env)...)
		out, err := cmd.CombinedOutput()
		if _, statErr := os.Stat(filepath.Join(cmd.Dir, "pwned")); statErr == nil {
			t.Fatal("a folder name was run as shell code")
		}
		return strings.TrimSpace(string(out)), err
	}

	if got, _ := run("maven: test", map[string]string{"LC_TARGET": "services/billing::api", "LC_ALSO_MAKE": "true", "LC_PROFILE": "local"}); got != "[-f][services/billing/pom.xml][-pl][api][-am][-Plocal][test]" {
		t.Errorf("module = %s", got)
	}
	if got, _ := run("maven: test", map[string]string{"LC_TARGET": "services/orders::", "LC_ALSO_MAKE": "true"}); got != "[-f][services/orders/pom.xml][test]" {
		t.Errorf("whole project = %s (no -pl, no -am)", got)
	}
	if got, _ := run("maven: package", map[string]string{"LC_TARGET": "x$(touch pwned)::"}); got != "[-f][x$(touch pwned)/pom.xml][package]" {
		t.Errorf("odd folder name = %s", got)
	}
	if got, _ := run("maven: spring-boot:run", map[string]string{"LC_TARGET": "services/billing::api", "LC_SPRING_PROFILES": "dev,local"}); got != "[-f][services/billing/pom.xml][-pl][api][-Dspring-boot.run.profiles=dev,local][spring-boot:run]" {
		t.Errorf("run goal = %s", got)
	}
	if got, err := run("maven: test", nil); err == nil || !strings.Contains(got, "no target picked") {
		t.Errorf("without a target: %q, %v", got, err)
	}
}

func TestMavenRootPomWithAnIndependentProject(t *testing.T) {
	root := t.TempDir()
	writePom(t, root, ".", `<modules><module>core</module></modules>`)
	writePom(t, root, "core", ``)
	writePom(t, root, "tools/gen", ``) // not a module of the root: a project of its own
	cmds, _ := Maven{}.Commands(root, nil)
	if got := strings.Join(targetOptions(mavenNames(cmds)["maven: compile"]), ","); got != ".::,.::core,tools/gen::" {
		t.Errorf("targets = %q", got)
	}
}

func TestMavenSingleRootProjectKeepsTheSimpleForm(t *testing.T) {
	root := t.TempDir()
	writePom(t, root, ".", `<modules><module>core</module><module>api</module></modules>`)
	writePom(t, root, "core", ``)
	writePom(t, root, "api", ``)
	cmds, _ := Maven{}.Commands(root, nil)
	test := mavenNames(cmds)["maven: test"]
	if targetOptions(test) != nil {
		t.Error("a single project with modules must keep module / also_make, not a target")
	}
	var module bool
	for _, p := range test.Params {
		module = module || p.Name == "module"
	}
	if !module {
		t.Errorf("params = %+v", test.Params)
	}
}

func TestMavenNestedProjectSearchLimits(t *testing.T) {
	root := t.TempDir()
	writePom(t, root, "a/b/c/d", ``)   // four folders down: searched
	writePom(t, root, "a/b/c/d/e", ``) // five: too deep
	if got := findProjects(root); len(got) != 1 || got[0].dir != "a/b/c/d" {
		t.Errorf("projects = %+v", got)
	}

	// A symlink out of the repo isn't followed.
	outside := t.TempDir()
	writePom(t, outside, "secret", ``)
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	for _, p := range findProjects(root) {
		if strings.HasPrefix(p.dir, "link") {
			t.Errorf("followed a symlink to %s", p.dir)
		}
	}
	if ok, _ := (Maven{}).Detect(t.TempDir()); ok {
		t.Error("an empty repo has no maven project")
	}
}

func TestWatchPathsCoverTheFilesFeaturesRead(t *testing.T) {
	root := t.TempDir()
	writePom(t, root, ".", `<modules><module>core</module></modules>`)
	writePom(t, root, "core", ``)
	writePom(t, root, "tools/gen", ``)
	writeFile(t, filepath.Join(root, "package.json"), `{"workspaces": ["apps/*"]}`)
	writeFile(t, filepath.Join(root, "apps", "web", "package.json"), `{"name": "web"}`)

	got := map[string]bool{}
	for _, p := range WatchPaths(root) {
		rel, _ := filepath.Rel(root, p)
		got[filepath.ToSlash(rel)] = true
	}
	for _, want := range []string{
		"pom.xml", "core/pom.xml", "tools/gen/pom.xml", "package.json", "apps/web/package.json",
		"compose.yaml", "docker-compose.yml", "pyproject.toml", "uv.lock", "pnpm-lock.yaml", "vite.config.ts",
	} {
		if !got[want] {
			t.Errorf("%s isn't watched; have %v", want, got)
		}
	}
}
