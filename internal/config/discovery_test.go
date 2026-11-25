package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeExecutable(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho hi\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func findCommand(groups []Group, groupTitle, name string) *ResolvedCommand {
	for _, g := range groups {
		if g.Title != groupTitle {
			continue
		}
		for i := range g.Commands {
			if g.Commands[i].Name == name {
				return &g.Commands[i]
			}
		}
	}
	return nil
}

func TestPlainScriptNeedsNoConfig(t *testing.T) {
	root := t.TempDir()
	writeExecutable(t, filepath.Join(root, "scripts", "clean-docker.sh"))

	groups, warnings := Discover(root, filepath.Join(root, "noproject"))

	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	cmd := findCommand(groups, "Global", "clean-docker")
	if cmd == nil {
		t.Fatalf("expected global command clean-docker, got groups %+v", groups)
	}
}

func TestSidecarUpgradesScript(t *testing.T) {
	root := t.TempDir()
	writeExecutable(t, filepath.Join(root, "scripts", "deploy-staging.sh"))
	writeFile(t, filepath.Join(root, "scripts", "deploy-staging.lazycmd.yaml"), `
desc: Deploy to staging
params:
  - { name: service, type: select, options_cmd: "ls services" }
`)

	groups, warnings := Discover(root, filepath.Join(root, "noproject"))
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	cmd := findCommand(groups, "Global", "deploy-staging")
	if cmd == nil {
		t.Fatalf("expected deploy-staging, got %+v", groups)
	}
	if cmd.Desc != "Deploy to staging" {
		t.Errorf("desc = %q", cmd.Desc)
	}
	if len(cmd.Params) != 1 || cmd.Params[0].Name != "service" {
		t.Errorf("params = %+v", cmd.Params)
	}
}

func TestBadSidecarIsSkippedWithWarning(t *testing.T) {
	root := t.TempDir()
	writeExecutable(t, filepath.Join(root, "scripts", "broken.sh"))
	writeFile(t, filepath.Join(root, "scripts", "broken.lazycmd.yaml"), `
desc: Broken
typo_field: true
`)

	groups, warnings := Discover(root, filepath.Join(root, "noproject"))
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %v", warnings)
	}
	if cmd := findCommand(groups, "Global", "broken"); cmd != nil {
		t.Errorf("expected broken command to be skipped, got %+v", cmd)
	}
}

func TestProjectCommandsYAML(t *testing.T) {
	proj := t.TempDir()
	writeFile(t, filepath.Join(proj, "commands.yaml"), `
commands:
  cloud-sql-proxy:
    desc: Start the Cloud SQL proxy
    params:
      - name: port
        type: string
        default: "5432"
    run: |
      exec cloud-sql-proxy --port "$LC_PORT"
  deploy:
    desc: Deploy a service
    script: ./scripts/deploy.sh
`)

	groups, warnings := Discover(filepath.Join(proj, "noglobal"), proj)
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}

	proxy := findCommand(groups, "This project", "cloud-sql-proxy")
	if proxy == nil {
		t.Fatalf("expected cloud-sql-proxy, got %+v", groups)
	}
	if proxy.Run == "" {
		t.Errorf("expected run to be set")
	}

	deploy := findCommand(groups, "This project", "deploy")
	if deploy == nil {
		t.Fatalf("expected deploy, got %+v", groups)
	}
	wantScript := filepath.Join(proj, "scripts", "deploy.sh")
	if deploy.ScriptPath != wantScript {
		t.Errorf("script path = %q, want %q", deploy.ScriptPath, wantScript)
	}
}

func TestCommandMissingRunOrScriptIsSkipped(t *testing.T) {
	proj := t.TempDir()
	writeFile(t, filepath.Join(proj, "commands.yaml"), `
commands:
  nothing:
    desc: Has neither run nor script
  both:
    desc: Has both, also invalid
    run: echo hi
    script: ./x.sh
`)

	groups, warnings := Discover(filepath.Join(proj, "noglobal"), proj)
	if len(warnings) != 2 {
		t.Fatalf("expected 2 warnings, got %v", warnings)
	}
	if len(groups) != 0 {
		t.Fatalf("expected no commands to load, got %+v", groups)
	}
}

func TestProjectCommandShadowsGlobal(t *testing.T) {
	root := t.TempDir()
	writeExecutable(t, filepath.Join(root, "scripts", "deploy.sh"))

	proj := t.TempDir()
	writeFile(t, filepath.Join(proj, "commands.yaml"), `
commands:
  deploy:
    desc: Project-specific deploy
    run: echo project-deploy
`)

	groups, warnings := Discover(root, proj)
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if cmd := findCommand(groups, "Global", "deploy"); cmd != nil {
		t.Errorf("expected global deploy to be shadowed, got %+v", cmd)
	}
	cmd := findCommand(groups, "This project", "deploy")
	if cmd == nil || cmd.Desc != "Project-specific deploy" {
		t.Errorf("expected project deploy to win, got %+v", cmd)
	}
}

func TestNoProjectDirIsFine(t *testing.T) {
	root := t.TempDir()
	groups, warnings := Discover(root, filepath.Join(root, "does-not-exist"))
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if len(groups) != 0 {
		t.Fatalf("expected no groups, got %+v", groups)
	}
}

func TestOldModeKeyIsNowAnError(t *testing.T) {
	proj := t.TempDir()
	writeFile(t, filepath.Join(proj, "commands.yaml"), `
commands:
  proxy:
    mode: background
    run: echo hi
`)
	groups, warnings := Discover(filepath.Join(proj, "noglobal"), proj)
	if findCommand(groups, "This project", "proxy") != nil {
		t.Error("a command still using the removed mode: key should not load silently")
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "mode") {
		t.Errorf("warnings = %v, want one naming the unknown mode field", warnings)
	}
}
