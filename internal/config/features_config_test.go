package config

import (
	"path/filepath"
	"testing"
)

func TestEnabledFeaturesSettingsMerge(t *testing.T) {
	global := t.TempDir()
	writeFile(t, filepath.Join(global, "config.yaml"), `
maven:
  default_profile: dev
`)
	project := t.TempDir()
	writeFile(t, filepath.Join(project, "config.yaml"), `
maven:
  default_profile: ci
`)

	_, settings, _, warnings := EnabledFeatures(global, project)
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if settings["maven"]["default_profile"] != "ci" {
		t.Errorf("maven.default_profile = %q, want project override \"ci\"", settings["maven"]["default_profile"])
	}
}

func TestEnabledFeaturesDisableAndReenable(t *testing.T) {
	global := t.TempDir()
	writeFile(t, filepath.Join(global, "config.yaml"), "disable: [git, vite]\n")
	project := t.TempDir()
	writeFile(t, filepath.Join(project, "config.yaml"), "features: [vite]\ndisable: [maven]\n")

	enabled, _, _, warnings := EnabledFeatures(global, project)
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	want := map[string]bool{"maven": false, "npm": true, "vite": true, "git": false}
	for name, on := range want {
		if enabled[name] != on {
			t.Errorf("enabled[%q] = %v, want %v (all: %v)", name, enabled[name], on, enabled)
		}
	}
}

func TestEnabledFeaturesAllOnByDefault(t *testing.T) {
	enabled, _, _, warnings := EnabledFeatures(t.TempDir(), t.TempDir())
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	for _, name := range AllFeatures {
		if !enabled[name] {
			t.Errorf("%q should be on by default, got %v", name, enabled)
		}
	}
}

func TestMavenGoalsLoadAndMerge(t *testing.T) {
	global, project := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(global, "config.yaml"), `
maven:
  default_profile: dev
  goals:
    - goal: spring-boot:run
      plugin: acme-boot-maven-plugin
`)
	writeFile(t, filepath.Join(project, "config.yaml"), `
maven:
  goals:
    - { goal: "clean install", run: false }
    - { goal: "acme:serve", run: true, profile_param: acme_env, profile_prop: acme.env }
`)
	_, settings, goals, warnings := EnabledFeatures(global, project)
	if len(warnings) != 0 {
		t.Fatalf("warnings: %v", warnings)
	}
	if settings["maven"]["default_profile"] != "dev" {
		t.Errorf("settings = %v", settings["maven"])
	}
	if len(goals) != 3 || goals[0].Plugin != "acme-boot-maven-plugin" || goals[1].Goal != "clean install" ||
		goals[2].Run == nil || !*goals[2].Run || goals[2].ProfileProp != "acme.env" {
		t.Errorf("goals = %+v", goals)
	}
}

func TestMavenGoalsRejectUnsafeAndUnknown(t *testing.T) {
	// config.yaml may come from a repo, and a goal ends up in a shell
	// command, so anything beyond goal-like words must be dropped.
	for _, goal := range []string{"test; touch /tmp/x", "$(id)", "a  b", "-D=x", "x`y`", "a\nb"} {
		g := MavenGoal{Goal: goal}
		if g.validate() == nil {
			t.Errorf("goal %q accepted", goal)
		}
	}
	for _, g := range []MavenGoal{
		{Goal: "run", Plugin: "a b"},
		{Goal: "run", ProfileParam: "x"},
		{Goal: "run", ProfileParam: "module", ProfileProp: "p"},
		{Goal: "run", ProfileParam: "x", ProfileProp: "a;b"},
		{Goal: "run", ProfileParam: "a-b", ProfileProp: "p"},
	} {
		if g.validate() == nil {
			t.Errorf("%+v accepted", g)
		}
	}

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "config.yaml"), `
maven:
  goals:
    - { goal: "ok:go" }
    - { goal: "bad; rm -rf ~" }
`)
	_, _, goals, warnings := EnabledFeatures(t.TempDir(), dir)
	if len(goals) != 1 || goals[0].Goal != "ok:go" || len(warnings) != 1 {
		t.Errorf("goals = %+v, warnings = %v", goals, warnings)
	}

	writeFile(t, filepath.Join(dir, "config.yaml"), "maven:\n  goals:\n    - { goal: x, plgin: y }\n")
	_, _, goals, warnings = EnabledFeatures(t.TempDir(), dir)
	if len(goals) != 0 || len(warnings) != 1 {
		t.Errorf("a typo'd key should fail the file: goals %+v, warnings %v", goals, warnings)
	}
}

func TestComposeAndPythonSettingsAndSwitches(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "config.yaml"), "compose:\n  command: podman compose\npython:\n  runner: uv\ndisable: [compose]\n")
	enabled, settings, _, warnings := EnabledFeatures(t.TempDir(), dir)
	if len(warnings) != 0 {
		t.Fatalf("warnings: %v", warnings)
	}
	if settings["compose"]["command"] != "podman compose" || settings["python"]["runner"] != "uv" {
		t.Errorf("settings = %v", settings)
	}
	if enabled["compose"] || !enabled["python"] {
		t.Errorf("enabled = %v", enabled)
	}
}
