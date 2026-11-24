package features

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"lazycast/internal/config"
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

	cmds, _ := Vite{}.Commands(t.TempDir(), nil)
	var devIsBackground bool
	for _, c := range cmds {
		if c.Name == "vite: dev" {
			devIsBackground = c.Mode == config.ModeBackground
		}
	}
	if !devIsBackground {
		t.Error("vite: dev should be background mode")
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
